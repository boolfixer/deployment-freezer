package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func setPhase(dfz *freezerv1alpha1.DeploymentFreezer, phase freezerv1alpha1.Phase) {
	dfz.Status.Phase = phase
}

func phaseForNotFound(dfz *freezerv1alpha1.DeploymentFreezer) freezerv1alpha1.Phase {
	// If we never started, it's Pending; if we were in-flight, Aborted.
	switch dfz.Status.Phase {
	case freezerv1alpha1.PhasePending, "":
		return freezerv1alpha1.PhasePending
	default:
		return freezerv1alpha1.PhaseAborted
	}
}

func setCondition(
	dfz *freezerv1alpha1.DeploymentFreezer,
	condType freezerv1alpha1.ConditionType,
	condStatus freezerv1alpha1.ConditionStatus,
	condReason freezerv1alpha1.ConditionReason,
	message string,
) {
	now := metav1.Now()

	conds := dfz.Status.Conditions
	newC := freezerv1alpha1.Condition{
		Type:               condType,
		Status:             condStatus,
		Reason:             condReason,
		Message:            message,
		LastTransitionTime: now,
	}

	replaced := false
	for i := range conds {
		if conds[i].Type == condType {
			if conds[i].Status != condStatus || conds[i].Reason != condReason || conds[i].Message != message {
				conds[i] = newC
			} else {
				// unchanged -> refresh transition time
				conds[i].LastTransitionTime = now
			}
			replaced = true
			break
		}
	}
	if !replaced {
		conds = append(conds, newC)
	}
	dfz.Status.Conditions = conds
}

func hashTemplate(d *appsv1.Deployment) string {
	h := sha256.New()
	// Hash the bits of spec that imply rollout: pod template and strategy
	fmt.Fprintf(h, "%v", d.Spec.Template.Spec)
	fmt.Fprintf(h, "%v", d.Spec.Template.Labels)
	fmt.Fprintf(h, "%v", d.Spec.Strategy)
	return hex.EncodeToString(h.Sum(nil))
}

func removeString(sl []string, s string) []string {
	out := sl[:0]
	for _, x := range sl {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
