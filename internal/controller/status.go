package controller

import (
	"context"
	"reflect"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type statusTracker struct {
	orig freezerv1alpha1.DeploymentFreezerStatus
}

func newStatusTracker(dfz *freezerv1alpha1.DeploymentFreezer) statusTracker {
	return statusTracker{orig: dfz.Status}
}

// commitStatus writes status once if it changed; uses retry on conflict with a fresh GET.
func (r *DeploymentFreezerReconciler) commitStatus(
	ctx context.Context,
	dfz *freezerv1alpha1.DeploymentFreezer,
	st statusTracker,
) {
	if reflect.DeepEqual(st.orig, dfz.Status) {
		return
	}
	err := retry.OnError(retry.DefaultRetry, func(err error) bool { return true }, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if err := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}, &latest); err != nil {
			return err
		}
		orig := latest.DeepCopy()
		latest.Status = dfz.Status
		return r.Status().Patch(ctx, &latest, client.MergeFrom(orig))
	})
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to update status")
	}
}
