package controller

import (
	"context"
	"reflect"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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
) error {
	if reflect.DeepEqual(st.orig, dfz.Status) {
		return nil
	}
	lg := log.FromContext(ctx)

	err := retry.OnError(retry.DefaultRetry, func(err error) bool { return true }, func() error {
		var latest freezerv1alpha1.DeploymentFreezer
		if getErr := r.Get(ctx, types.NamespacedName{Namespace: dfz.Namespace, Name: dfz.Name}, &latest); getErr != nil {
			return getErr
		}
		latest.Status = dfz.Status
		return r.Status().Update(ctx, &latest)
	})
	if err != nil {
		lg.Error(err, "failed to update status")
	}
	return err
}
