package controller

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"k8s.io/client-go/tools/record"
)

const (
	finalizerName        = "apps.boolfixer.dev/finalizer"
	annoFrozenBy         = "apps.boolfixer.dev/frozen-by"     // value: "<namespace>/<name>"
	annoTemplateHash     = "apps.boolfixer.dev/template-hash" // stored on DFZ .metadata.annotations for spec-change detection
	requeueShort         = 2 * time.Second
	requeueMedium        = 5 * time.Second
	defaultReplicasCount = int32(1)
)

// DeploymentFreezerReconciler reconciles a DeploymentFreezer object
type DeploymentFreezerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	now      func() time.Time
}

// RBAC markers (adjust group/name if they differ in your repo)
// +kubebuilder:rbac:groups=apps.boolfixer.dev,resources=deploymentfreezers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.boolfixer.dev,resources=deploymentfreezers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.boolfixer.dev,resources=deploymentfreezers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *DeploymentFreezerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx).WithValues("dfz", req.NamespacedName)
	ctx = log.IntoContext(ctx, lg)

	// Load DFZ
	var dfz freezerv1alpha1.DeploymentFreezer
	if err := r.Get(ctx, req.NamespacedName, &dfz); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Track status changes and write once at the end
	st := newStatusTracker(&dfz)
	defer func() { _ = r.commitStatus(ctx, &dfz, st) }()

	// Finalizer handling
	if dfz.ObjectMeta.DeletionTimestamp.IsZero() {
		if err := r.ensureFinalizer(ctx, &dfz); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		// Deletion: best-effort release and remove finalizer
		if res, err := r.reconcileDelete(ctx, &dfz); err != nil || !res.IsZero() {
			return res, err
		}
		if err := r.removeFinalizer(ctx, &dfz); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Validate target
	targetName := dfz.Spec.TargetRef.Name
	if targetName == "" {
		setPhase(&dfz, freezerv1alpha1.PhaseDenied)
		setCondition(
			&dfz,
			freezerv1alpha1.ConditionTypeTargetFound,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonNotFound,
			msgSpecTargetEmpty,
		)
		return ctrl.Result{}, nil
	}

	// Fetch the target Deployment
	var deploy appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: targetName}, &deploy); err != nil {
		if apierrors.IsNotFound(err) {
			setPhase(&dfz, phaseForNotFound(&dfz))
			setCondition(
				&dfz,
				freezerv1alpha1.ConditionTypeTargetFound,
				freezerv1alpha1.ConditionStatusFalse,
				freezerv1alpha1.ConditionReasonNotFound,
				msgTargetDeploymentNotExist,
			)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
		setCondition(
			&dfz,
			freezerv1alpha1.ConditionTypeHealth,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonAPIConflict,
			fmt.Sprintf(msgReadErrorFmt, err),
		)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// UID pinning / recreation detection
	if dfz.Status.TargetRef.UID != "" && deploy.UID != dfz.Status.TargetRef.UID {
		setCondition(
			&dfz,
			freezerv1alpha1.ConditionTypeTargetFound,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonUIDMismatch,
			msgUIDRecreated,
		)
		setPhase(&dfz, freezerv1alpha1.PhaseAborted)
		return ctrl.Result{}, nil
	}

	// Cache UID/name into status if not set
	if dfz.Status.TargetRef.UID == "" {
		dfz.Status.TargetRef.Name = deploy.Name
		dfz.Status.TargetRef.UID = deploy.UID
	}

	// Compute/remember template hash to detect spec changes while frozen
	if err := r.ensureTemplateHashAnno(ctx, &dfz, &deploy); err != nil {
		setCondition(
			&dfz,
			freezerv1alpha1.ConditionTypeHealth,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonAPIConflict,
			fmt.Sprintf(msgTemplateHashPatchFailedFmt, err),
		)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Record observedGeneration only after successfully processing current spec
	if dfz.Status.ObservedGeneration != dfz.GetGeneration() {
		dfz.Status.ObservedGeneration = dfz.GetGeneration()
	}

	// Phase router
	if dfz.Status.Phase == "" {
		setPhase(&dfz, freezerv1alpha1.PhasePending)
	}

	switch dfz.Status.Phase {
	case freezerv1alpha1.PhasePending, freezerv1alpha1.PhaseFreezing:
		return r.handlePendingOrFreezing(ctx, &dfz, &deploy)
	case freezerv1alpha1.PhaseFrozen:
		return r.handleFrozen(ctx, &dfz, &deploy)
	case freezerv1alpha1.PhaseUnfreezing:
		return r.handleUnfreezing(ctx, &dfz, &deploy)
	case freezerv1alpha1.PhaseDenied, freezerv1alpha1.PhaseCompleted, freezerv1alpha1.PhaseAborted:
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}
}

func (r *DeploymentFreezerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.now = func() time.Time { return time.Now().UTC() }

	// 1) Index fields for efficient lookups
	if err := r.setupFieldIndex(context.Background(), mgr); err != nil {
		return err
	}

	// 2) Build controller and register watches
	startupCh := make(chan event.GenericEvent)
	if _, err := r.buildController(mgr, startupCh); err != nil {
		return err
	}

	// 3) Initialize event recorder for this controller
	r.Recorder = mgr.GetEventRecorderFor("deployment-freezer")

	// 4) Register a startup runnable to enqueue overdue frozen items
	if err := r.registerStartupRunnable(mgr, startupCh); err != nil {
		return err
	}

	return nil
}

func (r *DeploymentFreezerReconciler) setupFieldIndex(ctx context.Context, mgr ctrl.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		ctx,
		&freezerv1alpha1.DeploymentFreezer{},
		".spec.targetRef.name",
		func(raw client.Object) []string {
			dfz := raw.(*freezerv1alpha1.DeploymentFreezer)
			if dfz.Spec.TargetRef.Name == "" {
				return nil
			}
			return []string{dfz.Spec.TargetRef.Name}
		},
	)
}

func (r *DeploymentFreezerReconciler) buildController(mgr ctrl.Manager, startupCh <-chan event.GenericEvent) (controller.Controller, error) {
	return ctrl.NewControllerManagedBy(mgr).
		For(&freezerv1alpha1.DeploymentFreezer{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.deploymentToDFZMapper),
			// Only react to Deployment spec changes (generation changes), ignore status-only updates
			builder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		// Watch a channel so we can push GenericEvents on startup
		WatchesRawSource(source.Channel(startupCh, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Build(r)
}

func (r *DeploymentFreezerReconciler) deploymentToDFZMapper(ctx context.Context, obj client.Object) []reconcile.Request {
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return nil
	}

	// List DFZs targeting this Deployment name (same namespace), using the field index
	var list freezerv1alpha1.DeploymentFreezerList
	if err := r.List(
		ctx,
		&list,
		client.InNamespace(d.Namespace),
		client.MatchingFields{".spec.targetRef.name": d.Name},
	); err != nil {
		return nil
	}

	reqs := make([]reconcile.Request, len(list.Items))
	for i := range list.Items {
		reqs[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: list.Items[i].Namespace,
				Name:      list.Items[i].Name,
			},
		}
	}
	return reqs
}

func (r *DeploymentFreezerReconciler) registerStartupRunnable(mgr ctrl.Manager, startupCh chan event.GenericEvent) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		// Ensure cache is synced before we list
		if ok := mgr.GetCache().WaitForCacheSync(ctx); !ok {
			return ctx.Err()
		}

		var list freezerv1alpha1.DeploymentFreezerList
		if err := r.List(ctx, &list); err != nil {
			return err
		}

		now := r.now()
		for i := range list.Items {
			dfz := list.Items[i]
			if dfz.Status.Phase == freezerv1alpha1.PhaseFrozen &&
				dfz.Status.FreezeUntil != nil &&
				!dfz.Status.FreezeUntil.Time.After(now) {
				// Push a GenericEvent to enqueue this object immediately
				// Important: pass a pointer to a distinct object per loop
				obj := dfz // copy
				startupCh <- event.GenericEvent{Object: &obj}
			}
		}

		// Close the channel to avoid leaks; watch remains registered but idle.
		close(startupCh)
		return nil
	}))
}
