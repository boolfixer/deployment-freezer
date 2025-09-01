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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// patchDeploymentReplicas sets .spec.replicas using a MergeFrom patch with retry on conflict.
func (r *DeploymentFreezerReconciler) patchDeploymentReplicas(
	ctx context.Context,
	d *appsv1.Deployment,
	replicas int32,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest appsv1.Deployment
		if err := r.Get(ctx, types.NamespacedName{Namespace: d.Namespace, Name: d.Name}, &latest); err != nil {
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
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}, &latest); err != nil {
			return err
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
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}, &latest); err != nil {
			return err
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
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest freezerv1alpha1.DeploymentFreezer
			if err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}, &latest); err != nil {
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

func (r *DeploymentFreezerReconciler) reconcileDelete(
	ctx context.Context,
	deployment *appsv1.Deployment,
	dfz *freezerv1alpha1.DeploymentFreezer,
) {
	owner := fmt.Sprintf("%s/%s", dfz.Namespace, dfz.Name)
	if deployment.Annotations[annoFrozenBy] != owner {
		// We are not the owner anymore; nothing to do.
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonSkippedNotOwner, msgSkippedNotOwner, owner)
		return
	}

	// Restore replicas
	replicas := defaultReplicasCount
	if dfz.Status.OriginalReplicas != nil {
		replicas = *dfz.Status.OriginalReplicas
	}
	if err := r.patchDeploymentReplicas(ctx, deployment, replicas); err != nil {
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonRestoreFailed, msgReplicasRestoreFailed, replicas, err)
	} else {
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonRestored, msgReplicasRestored, replicas)
	}

	// Clear ownership annotation
	if err := r.patchDeploymentAnno(ctx, deployment, annoFrozenBy, ""); err != nil {
		r.Recorder.Eventf(dfz, corev1.EventTypeWarning, ReasonClearOwnershipFailed, msgClearOwnershipFailed, err)
	} else {
		r.Recorder.Eventf(dfz, corev1.EventTypeNormal, ReasonOwnershipCleared, msgOwnershipCleared, deployment.Namespace, deployment.Name)
	}
}
