package controller

import (
	"testing"
	"time"

	freezerv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSetCondition(t *testing.T) {
	t.Run("AppendNewCondition_WhenAbsent", func(t *testing.T) {
		t.Parallel()

		dfz := &freezerv1alpha1.DeploymentFreezer{}
		typ := freezerv1alpha1.ConditionType("TypeA")
		status := freezerv1alpha1.ConditionStatus("True")
		reason := freezerv1alpha1.ConditionReason("Started")
		msg := "freeze started"

		before := time.Now()
		setCondition(dfz, typ, status, reason, msg)
		after := time.Now()

		assert.Len(t, dfz.Status.Conditions, 1)
		c := dfz.Status.Conditions[0]
		assert.Equal(t, typ, c.Type)
		assert.Equal(t, status, c.Status)
		assert.Equal(t, reason, c.Reason)
		assert.Equal(t, msg, c.Message)
		assert.False(t, c.LastTransitionTime.IsZero(), "LastTransitionTime should be set")
		assert.True(t, !c.LastTransitionTime.Time.Before(before) && !c.LastTransitionTime.Time.After(after),
			"LastTransitionTime should be within call window")
	})

	t.Run("ReplaceExistingCondition_WhenFieldsDiffer", func(t *testing.T) {
		t.Parallel()

		typ := freezerv1alpha1.ConditionType("TypeB")
		old := freezerv1alpha1.Condition{
			Type:               typ,
			Status:             freezerv1alpha1.ConditionStatus("False"),
			Reason:             freezerv1alpha1.ConditionReason("OldReason"),
			Message:            "old message",
			LastTransitionTime: metav1.NewTime(time.Unix(1_600_000_000, 0).UTC()),
		}
		dfz := &freezerv1alpha1.DeploymentFreezer{
			Status: freezerv1alpha1.DeploymentFreezerStatus{
				Conditions: []freezerv1alpha1.Condition{old},
			},
		}

		newStatus := freezerv1alpha1.ConditionStatus("True")
		newReason := freezerv1alpha1.ConditionReason("NewReason")
		newMsg := "new message"

		before := time.Now()
		setCondition(dfz, typ, newStatus, newReason, newMsg)
		after := time.Now()

		assert.Len(t, dfz.Status.Conditions, 1)
		got := dfz.Status.Conditions[0]
		assert.Equal(t, typ, got.Type)
		assert.Equal(t, newStatus, got.Status)
		assert.Equal(t, newReason, got.Reason)
		assert.Equal(t, newMsg, got.Message)
		assert.True(t, got.LastTransitionTime.Time.After(old.LastTransitionTime.Time))
		assert.True(t, !got.LastTransitionTime.Time.Before(before) && !got.LastTransitionTime.Time.After(after))
	})

	t.Run("RefreshTransitionTime_WhenUnchanged", func(t *testing.T) {
		t.Parallel()

		typ := freezerv1alpha1.ConditionType("TypeC")
		status := freezerv1alpha1.ConditionStatus("Unknown")
		reason := freezerv1alpha1.ConditionReason("Waiting")
		msg := "no change"

		oldTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))
		dfz := &freezerv1alpha1.DeploymentFreezer{
			Status: freezerv1alpha1.DeploymentFreezerStatus{
				Conditions: []freezerv1alpha1.Condition{{
					Type:               typ,
					Status:             status,
					Reason:             reason,
					Message:            msg,
					LastTransitionTime: oldTime,
				}},
			},
		}

		setCondition(dfz, typ, status, reason, msg)

		assert.Len(t, dfz.Status.Conditions, 1)
		got := dfz.Status.Conditions[0]
		assert.Equal(t, typ, got.Type)
		assert.Equal(t, status, got.Status)
		assert.Equal(t, reason, got.Reason)
		assert.Equal(t, msg, got.Message)
		assert.True(t, got.LastTransitionTime.Time.After(oldTime.Time), "transition time should be refreshed (greater than old)")
	})

	t.Run("OnlyTargetConditionUpdated_AmongMany", func(t *testing.T) {
		t.Parallel()

		typeA := freezerv1alpha1.ConditionType("TypeA")
		typeB := freezerv1alpha1.ConditionType("TypeB")

		aOldTime := metav1.NewTime(time.Now().Add(-10 * time.Minute))
		bOldTime := metav1.NewTime(time.Now().Add(-8 * time.Minute))

		dfz := &freezerv1alpha1.DeploymentFreezer{
			Status: freezerv1alpha1.DeploymentFreezerStatus{
				Conditions: []freezerv1alpha1.Condition{
					{
						Type:               typeA,
						Status:             freezerv1alpha1.ConditionStatus("True"),
						Reason:             freezerv1alpha1.ConditionReason("AReason"),
						Message:            "A msg",
						LastTransitionTime: aOldTime,
					},
					{
						Type:               typeB,
						Status:             freezerv1alpha1.ConditionStatus("False"),
						Reason:             freezerv1alpha1.ConditionReason("BOld"),
						Message:            "B old",
						LastTransitionTime: bOldTime,
					},
				},
			},
		}

		// Update only TypeB and ensure TypeA remains untouched.
		newBStatus := freezerv1alpha1.ConditionStatus("True")
		newBReason := freezerv1alpha1.ConditionReason("BNew")
		newBMsg := "B new"

		setCondition(dfz, typeB, newBStatus, newBReason, newBMsg)

		assert.Len(t, dfz.Status.Conditions, 2)

		// Find A and B
		var condA, condB *freezerv1alpha1.Condition
		for i := range dfz.Status.Conditions {
			switch dfz.Status.Conditions[i].Type {
			case typeA:
				condA = &dfz.Status.Conditions[i]
			case typeB:
				condB = &dfz.Status.Conditions[i]
			}
		}
		if assert.NotNil(t, condA) && assert.NotNil(t, condB) {
			// A unchanged
			assert.Equal(t, freezerv1alpha1.ConditionStatus("True"), condA.Status)
			assert.Equal(t, freezerv1alpha1.ConditionReason("AReason"), condA.Reason)
			assert.Equal(t, "A msg", condA.Message)
			assert.True(t, condA.LastTransitionTime.Equal(&aOldTime), "TypeA LTT should not change")

			// B updated
			assert.Equal(t, newBStatus, condB.Status)
			assert.Equal(t, newBReason, condB.Reason)
			assert.Equal(t, newBMsg, condB.Message)
			assert.True(t, condB.LastTransitionTime.Time.After(bOldTime.Time), "TypeB LTT should be updated")
		}
	})
}

func TestSetPhase(t *testing.T) {
	t.Run("SetToPending", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{}
		setPhase(dfz, freezerv1alpha1.PhasePending)
		assert.Equal(t, freezerv1alpha1.PhasePending, dfz.Status.Phase)
	})

	t.Run("OverwriteExistingPhase", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{Status: freezerv1alpha1.DeploymentFreezerStatus{Phase: freezerv1alpha1.PhaseAborted}}
		setPhase(dfz, freezerv1alpha1.PhaseFrozen)
		assert.Equal(t, freezerv1alpha1.PhaseFrozen, dfz.Status.Phase)
	})
}

func TestPhaseForNotFound(t *testing.T) {
	t.Run("EmptyPhase_ReturnsPending", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{}
		got := phaseForNotFound(dfz)
		assert.Equal(t, freezerv1alpha1.PhasePending, got)
	})

	t.Run("Pending_ReturnsPending", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{Status: freezerv1alpha1.DeploymentFreezerStatus{Phase: freezerv1alpha1.PhasePending}}
		got := phaseForNotFound(dfz)
		assert.Equal(t, freezerv1alpha1.PhasePending, got)
	})

	t.Run("Frozen_ReturnsAborted", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{Status: freezerv1alpha1.DeploymentFreezerStatus{Phase: freezerv1alpha1.PhaseFrozen}}
		got := phaseForNotFound(dfz)
		assert.Equal(t, freezerv1alpha1.PhaseAborted, got)
	})

	t.Run("Freezing_ReturnsAborted", func(t *testing.T) {
		t.Parallel()
		dfz := &freezerv1alpha1.DeploymentFreezer{Status: freezerv1alpha1.DeploymentFreezerStatus{Phase: freezerv1alpha1.PhaseFreezing}}
		got := phaseForNotFound(dfz)
		assert.Equal(t, freezerv1alpha1.PhaseAborted, got)
	})
}

func TestHashTemplate(t *testing.T) {
	newBaseDeployment := func() *appsv1.Deployment {
		return &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"app": "web"},
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "c",
							Image: "busybox",
						}},
					},
				},
				Strategy: appsv1.DeploymentStrategy{},
			},
		}
	}

	t.Run("SameDeployment_SameHash", func(t *testing.T) {
		t.Parallel()
		d := newBaseDeployment()
		h1 := hashTemplate(d)
		h2 := hashTemplate(d.DeepCopy())
		assert.Equal(t, h1, h2)
	})

	t.Run("ChangeTemplateSpec_ChangesHash", func(t *testing.T) {
		t.Parallel()
		d := newBaseDeployment()
		h1 := hashTemplate(d)
		d2 := d.DeepCopy()
		d2.Spec.Template.Spec.Containers[0].Image = "nginx:latest"
		h2 := hashTemplate(d2)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("ChangeTemplateLabels_ChangesHash", func(t *testing.T) {
		t.Parallel()
		d := newBaseDeployment()
		h1 := hashTemplate(d)
		d2 := d.DeepCopy()
		d2.Spec.Template.Labels["env"] = "prod"
		h2 := hashTemplate(d2)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("ChangeStrategy_ChangesHash", func(t *testing.T) {
		t.Parallel()
		d := newBaseDeployment()
		h1 := hashTemplate(d)
		d2 := d.DeepCopy()
		one := intstr.FromInt(1)
		d2.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: &one,
			},
		}
		h2 := hashTemplate(d2)
		assert.NotEqual(t, h1, h2)
	})

	t.Run("UnrelatedFields_NoChange", func(t *testing.T) {
		t.Parallel()
		d := newBaseDeployment()
		h1 := hashTemplate(d)
		d2 := d.DeepCopy()
		d2.ObjectMeta.Name = "other-name"
		if d2.ObjectMeta.Annotations == nil {
			d2.ObjectMeta.Annotations = map[string]string{}
		}
		d2.ObjectMeta.Annotations["note"] = "does-not-affect-hash"
		h2 := hashTemplate(d2)
		assert.Equal(t, h1, h2)
	})
}

func TestRemoveString(t *testing.T) {
	t.Run("RemoveExisting_OneOrMore", func(t *testing.T) {
		t.Parallel()
		in := []string{"a", "b", "c", "b", "d"}
		out := removeString(in, "b")
		assert.Equal(t, []string{"a", "c", "d"}, out)
	})

	t.Run("RemoveNonExisting_NoChange", func(t *testing.T) {
		t.Parallel()
		in := []string{"a", "b", "c"}
		out := removeString(in, "x")
		assert.Equal(t, []string{"a", "b", "c"}, out)
	})

	t.Run("RemoveAll_EmptyResult", func(t *testing.T) {
		t.Parallel()
		in := []string{"x", "x", "x"}
		out := removeString(in, "x")
		assert.Empty(t, out)
	})

	t.Run("EmptyInput_EmptyResult", func(t *testing.T) {
		t.Parallel()
		var in []string
		out := removeString(in, "x")
		assert.Empty(t, out)
	})

	t.Run("OrderPreserved_AfterRemoval", func(t *testing.T) {
		t.Parallel()
		in := []string{"keep1", "rm", "keep2", "rm", "keep3"}
		out := removeString(in, "rm")
		assert.Equal(t, []string{"keep1", "keep2", "keep3"}, out)
	})
}
