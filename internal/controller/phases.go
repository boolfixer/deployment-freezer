package controller

import (
	"context"
	"fmt"
	"time"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// handlePendingOrFreezing acquires ownership and scales down to zero.
func (r *DeploymentFreezerReconciler) handlePendingOrFreezing(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
	deploy *appsv1.Deployment,
) (ctrl.Result, error) {
	owner := fmt.Sprintf("%s/%s", dfz.Namespace, dfz.Name)
	frozenBy := deploy.Annotations[annoFrozenBy]

	// Try to acquire ownership
	if frozenBy != "" && frozenBy != owner {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeOwnership,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonDeniedAlreadyFrozen,
			fmt.Sprintf(msgDeploymentAlreadyOwnedFmt, frozenBy),
		)
		setPhase(dfz, freezerv1alpha1.PhaseDenied)
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonOwnershipDenied, msgEvtOwnershipDenied, deploy.Namespace, deploy.Name, frozenBy)
		return ctrl.Result{}, nil
	}

	if frozenBy != owner {
		if err := r.patchDeploymentAnno(ctx, deploy, annoFrozenBy, owner); err != nil {
			setCondition(
				dfz,
				freezerv1alpha1.ConditionTypeHealth,
				freezerv1alpha1.ConditionStatusFalse,
				freezerv1alpha1.ConditionReasonAPIConflict,
				fmt.Sprintf(msgCannotScaleDownYetFmt, err),
			)
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeOwnership,
			freezerv1alpha1.ConditionStatusTrue,
			freezerv1alpha1.ConditionReasonAcquired,
			fmt.Sprintf(msgOwnershipAcquiredFmt, dfz.Name, deploy.Namespace, deploy.Name),
		)
	} else {
		// Ownership already held by this DFZ; nothing else to do in Pending.
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeOwnership,
			freezerv1alpha1.ConditionStatusTrue,
			freezerv1alpha1.ConditionReasonAcquired,
			msgOwnershipAlreadyHeld,
		)
	}

	// Record original replicas (prefer positive values; fall back to default)

	if dfz.Status.OriginalReplicas == nil {
		replicas := defaultReplicasCount
		if deploy.Spec.Replicas != nil && *deploy.Spec.Replicas > 0 {
			replicas = *deploy.Spec.Replicas
		}
		dfz.Status.OriginalReplicas = &replicas
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonUnfreezingStarted, fmt.Sprintf("replicas calculated: %d", replicas))
	}

	// Scale to zero
	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 0 {
		if err := r.patchDeploymentReplicas(ctx, deploy, 0); err != nil {
			setCondition(
				dfz,
				freezerv1alpha1.ConditionTypeFreezeProgress,
				freezerv1alpha1.ConditionStatusFalse,
				freezerv1alpha1.ConditionReasonAwaitingPDB,
				fmt.Sprintf(msgCannotScaleDownYetFmt, err),
			)
			setPhase(dfz, freezerv1alpha1.PhaseFreezing)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeFreezeProgress,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonScalingDown,
			msgScalingDeploymentToZero,
		)
		setPhase(dfz, freezerv1alpha1.PhaseFreezing)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Spec is 0; verify the Deployment is effectively at zero (no replicas running/ready/available/updated).
	if deploy.Status.Replicas == 0 &&
		deploy.Status.ReadyReplicas == 0 &&
		deploy.Status.AvailableReplicas == 0 &&
		deploy.Status.UpdatedReplicas == 0 {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeFreezeProgress,
			freezerv1alpha1.ConditionStatusTrue,
			freezerv1alpha1.ConditionReasonScaledToZero,
			msgDeploymentFullyScaledToZero,
		)
		setPhase(dfz, freezerv1alpha1.PhaseFrozen)
		until := r.now().Add(time.Duration(dfz.Spec.DurationSeconds) * time.Second)
		t := metav1.NewTime(until)
		dfz.Status.FreezeUntil = &t

		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonFrozen, msgEvtFrozenUntil, until.UTC().Format(time.RFC3339))
		return ctrl.Result{RequeueAfter: time.Until(until)}, nil
	}

	// Still draining/terminating: stay in Freezing until status catches up.
	setCondition(
		dfz,
		freezerv1alpha1.ConditionTypeFreezeProgress,
		freezerv1alpha1.ConditionStatusFalse,
		freezerv1alpha1.ConditionReasonScalingDown,
		msgWaitingDeploymentReachZero,
	)
	setPhase(dfz, freezerv1alpha1.PhaseFreezing)
	return ctrl.Result{RequeueAfter: requeueShort}, nil
}

// handleFrozen enforces ownership while waiting for unfreeze time.
func (r *DeploymentFreezerReconciler) handleFrozen(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
	deploy *appsv1.Deployment,
) (ctrl.Result, error) {
	owner := fmt.Sprintf("%s/%s", dfz.Namespace, dfz.Name)
	frozenBy := deploy.Annotations[annoFrozenBy]
	if frozenBy != owner {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeOwnership,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonLost,
			msgOwnershipAnnotationLost,
		)
		setPhase(dfz, freezerv1alpha1.PhaseAborted)
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonOwnershipLost, msgEvtOwnershipLost, deploy.Namespace, deploy.Name)
		return ctrl.Result{}, nil
	}

	if r.now().Before(dfz.Status.FreezeUntil.Time) {
		return ctrl.Result{RequeueAfter: time.Until(dfz.Status.FreezeUntil.Time)}, nil
	}

	setPhase(dfz, freezerv1alpha1.PhaseUnfreezing)
	r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonUnfreezingStarted, msgEvtUnfreezingStarted)
	return ctrl.Result{RequeueAfter: requeueShort}, nil
}

// handleUnfreezing restores replicas and releases ownership.
func (r *DeploymentFreezerReconciler) handleUnfreezing(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
	deploy *appsv1.Deployment,
) (ctrl.Result, error) {
	// Restore from the recorded original replicas; the current spec is 0 while frozen.
	targetReplicas := *dfz.Status.OriginalReplicas
	r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonUnfreezeCompleted, fmt.Sprintf("replicas to restore: %d", targetReplicas))

	if err := r.patchDeploymentReplicas(ctx, deploy, targetReplicas); err != nil {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeUnfreezeProgress,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonQuotaExceeded,
			fmt.Sprintf(msgFailedRestoreReplicasFmt, targetReplicas, err),
		)
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	}

	if err := r.patchDeploymentAnno(ctx, deploy, annoFrozenBy, ""); err != nil {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeHealth,
			freezerv1alpha1.ConditionStatusFalse,
			freezerv1alpha1.ConditionReasonAPIConflict,
			fmt.Sprintf(msgFailedClearOwnershipFmt, err),
		)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	setCondition(
		dfz, freezerv1alpha1.ConditionTypeUnfreezeProgress,
		freezerv1alpha1.ConditionStatusTrue,
		freezerv1alpha1.ConditionReasonScaledUp,
		fmt.Sprintf(msgDeploymentRestoredReplicasFmt, targetReplicas),
	)
	setCondition(
		dfz,
		freezerv1alpha1.ConditionTypeOwnership,
		freezerv1alpha1.ConditionStatusFalse,
		freezerv1alpha1.ConditionReasonReleased,
		msgOwnershipReleasedAfterUnfreeze,
	)
	setPhase(dfz, freezerv1alpha1.PhaseCompleted)
	r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonUnfreezeCompleted, msgEvtUnfreezeCompleted, targetReplicas)

	return ctrl.Result{}, nil
}
