package controller

import (
	"context"
	"fmt"
	"slices"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// patchDeploymentReplicas sets .spec.replicas using a MergeFrom patch with retry on conflict.
func (r *DeploymentFreezerReconciler) patchDeploymentReplicas(
	ctx context.Context,
	d *appsv1.Deployment,
	replicas int32,
) error {
	nn := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest appsv1.Deployment
		if err := r.Get(ctx, nn, &latest); err != nil {
			return err
		}
		orig := latest.DeepCopy()
		latest.Spec.Replicas = ptr.To(replicas)
		return r.Patch(ctx, &latest, client.MergeFrom(orig))
	})
}

// patchDeploymentAnno sets or clears a single annotation on Deployment using a MergeFrom patch with retry.
func (r *DeploymentFreezerReconciler) patchDeploymentAnno(
	ctx context.Context,
	d *appsv1.Deployment,
	key, val string,
) error {
	nn := types.NamespacedName{Namespace: d.Namespace, Name: d.Name}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest appsv1.Deployment
		if err := r.Get(ctx, nn, &latest); err != nil {
			return err
		}
		orig := latest.DeepCopy()
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		if val != "" {
			latest.Annotations[key] = val
		} else {
			delete(latest.Annotations, key)
		}
		return r.Patch(ctx, &latest, client.MergeFrom(orig))
	})
}

// ensureFinalizer adds the controller finalizer via Patch with retry to minimize conflicts.
func (r *DeploymentFreezerReconciler) ensureFinalizer(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
) error {
	if slices.Contains(dfz.Finalizers, finalizerName) {
		return nil
	}
	nn := types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if err := r.Get(ctx, nn, &latest); err != nil {
			return err
		}
		if slices.Contains(latest.Finalizers, finalizerName) {
			return nil
		}
		orig := latest.DeepCopy()
		latest.Finalizers = append(latest.Finalizers, finalizerName)
		return r.Patch(ctx, &latest, client.MergeFrom(orig))
	})
}

// removeFinalizer removes the controller finalizer via Patch with retry.
func (r *DeploymentFreezerReconciler) removeFinalizer(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
) error {
	if !slices.Contains(dfz.Finalizers, finalizerName) {
		return nil
	}
	nn := types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if err := r.Get(ctx, nn, &latest); err != nil {
			return err
		}
		if !slices.Contains(latest.Finalizers, finalizerName) {
			return nil
		}
		orig := latest.DeepCopy()
		latest.Finalizers = removeString(latest.Finalizers, finalizerName)
		return r.Patch(ctx, &latest, client.MergeFrom(orig))
	})
}

// ensureTemplateHashAnno initializes template-hash annotation, and flags spec change condition.
// Uses retry-on-conflict when first setting the annotation.
func (r *DeploymentFreezerReconciler) ensureTemplateHashAnno(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
	deploy *appsv1.Deployment,
) error {
	tplHash := hashTemplate(deploy)
	prevHash := ""
	if dfz.Annotations != nil {
		prevHash = dfz.Annotations[annoTemplateHash]
	}
	if prevHash == "" {
		nn := types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest freezerv1alpha1.DeploymentFreezer
			if err := r.Get(ctx, nn, &latest); err != nil {
				return err
			}
			if latest.Annotations == nil {
				latest.Annotations = map[string]string{}
			}
			if _, exists := latest.Annotations[annoTemplateHash]; exists {
				return nil
			}
			orig := latest.DeepCopy()
			latest.Annotations[annoTemplateHash] = tplHash
			return r.Patch(ctx, &latest, client.MergeFrom(orig))
		})
	}

	// If the stored hash differs from current template, raise a condition.
	if prevHash != tplHash {
		setCondition(
			dfz,
			freezerv1alpha1.ConditionTypeSpecChangedDuringFreeze,
			freezerv1alpha1.ConditionStatusTrue,
			freezerv1alpha1.ConditionReasonObserved,
			msgSpecChangedDuringFreeze,
		)
	}
	return nil
}

//nolint:unparam // keep (Result, error) signature for consistency and future flexibility
func (r *DeploymentFreezerReconciler) reconcileDelete(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
) (ctrl.Result, error) {
	// Best-effort release: if we still own the Deployment, try to remove annotation and restore replicas.
	var deploy appsv1.Deployment
	if dfz.Spec.TargetRef.Name == "" {
		// Nothing to release
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonReleaseSkippedNoTarget, msgEvtReleaseSkippedNoTarget)
		return ctrl.Result{}, nil
	}
	err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Spec.TargetRef.Name}, &deploy)
	if err != nil {
		// If not found, inform and exit quietly
		if client.IgnoreNotFound(err) == nil {
			r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonReleaseSkippedNotFound, msgEvtReleaseSkippedNotFound, dfz.Namespace, dfz.Spec.TargetRef.Name)
			return ctrl.Result{}, nil
		}
		// Other errors: surface and let controller retry
		return ctrl.Result{}, err
	}

	owner := fmt.Sprintf("%s/%s", dfz.Namespace, dfz.Name)
	if deploy.Annotations[annoFrozenBy] != owner {
		// We are not the owner anymore; nothing to do.
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonReleaseSkippedNotOwner, msgEvtReleaseSkippedNotOwner, owner)
		return ctrl.Result{}, nil
	}

	// Restore replicas (best-effort)
	replicas := defaultReplicasCount
	if dfz.Status.OriginalReplicas != nil {
		replicas = *dfz.Status.OriginalReplicas
	}
	if err := r.patchDeploymentReplicas(ctx, &deploy, replicas); err != nil {
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonReleaseRestoreFailed, msgEvtReleaseRestoreFailed, replicas, err)
	} else {
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonReleaseRestored, msgEvtReleaseRestored, replicas)
	}

	// Clear ownership annotation (best-effort)
	if err := r.patchDeploymentAnno(ctx, &deploy, annoFrozenBy, ""); err != nil {
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonReleaseClearOwnershipFailed, msgEvtReleaseClearOwnershipFailed, err)
	} else {
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonReleaseOwnershipCleared, msgEvtReleaseOwnershipCleared, deploy.Namespace, deploy.Name)
	}

	// No status updates on delete path
	return ctrl.Result{}, nil
}
