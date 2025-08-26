/*
// Copyright header omitted for brevity; preserved by VCS
*/

package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1alpha1 "github.com/boolfixer/deployment-freezer/api/v1alpha1"
)

var _ = Describe("DeploymentFreezer Controller", Ordered, func() {
	const (
		ns           = "default"
		deployName   = "demo-deploy"
		dfzName      = "freeze-demo"
		otherOwner   = "default/other"
		origReplicas = int32(3)
	)

	var (
		ctx context.Context
	)

	newReconciler := func(now time.Time) *DeploymentFreezerReconciler {
		r := &DeploymentFreezerReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(64),
			now:      func() time.Time { return now },
		}
		return r
	}

	makeDeployment := func(name string, replicas int32, anno map[string]string) *appsv1.Deployment {
		if anno == nil {
			anno = map[string]string{}
		}
		labels := map[string]string{"app": name}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   ns,
				Name:        name,
				Annotations: anno,
				Labels:      labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: pointer.Int32(replicas),
				Selector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "nginx",
							Image: "nginx:1.25",
						}},
					},
				},
			},
		}
	}

	makeDFZ := func(name, target string, durationSeconds int64) *appsv1alpha1.DeploymentFreezer {
		return &appsv1alpha1.DeploymentFreezer{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      name,
			},
			Spec: appsv1alpha1.DeploymentFreezerSpec{
				TargetRef:       appsv1alpha1.DeploymentTargetRef{Name: target},
				DurationSeconds: durationSeconds,
			},
		}
	}

	get := func(obj types.NamespacedName, res interface{}) error {
		switch r := res.(type) {
		case *appsv1.Deployment:
			return k8sClient.Get(ctx, obj, r)
		case *appsv1alpha1.DeploymentFreezer:
			return k8sClient.Get(ctx, obj, r)
		default:
			return fmt.Errorf("unsupported type")
		}
	}

	BeforeAll(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		// Ensure DFZ is fully removed, even if it has finalizers (since we don't run a manager loop here)
		keyDFZ := types.NamespacedName{Namespace: ns, Name: dfzName}
		var dfz appsv1alpha1.DeploymentFreezer
		if err := k8sClient.Get(ctx, keyDFZ, &dfz); err == nil {
			if len(dfz.Finalizers) > 0 {
				dfz.Finalizers = nil
				_ = k8sClient.Update(ctx, &dfz)
			}
			_ = k8sClient.Delete(ctx, &dfz)
		}
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyDFZ, &appsv1alpha1.DeploymentFreezer{})
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		// Ensure Deployment is fully removed as well
		keyDep := types.NamespacedName{Namespace: ns, Name: deployName}
		_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: deployName}})
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyDep, &appsv1.Deployment{})
			return apierrors.IsNotFound(err)
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
	})

	It("sets TargetFound=false and Phase=Pending when target Deployment does not exist", func() {
		By("creating DFZ that references a missing Deployment")
		dfz := makeDFZ(dfzName, "does-not-exist", 5)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		// Verify status updated
		var refreshed appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &refreshed)).To(Succeed())
		Expect(refreshed.Status.Phase).To(Equal(appsv1alpha1.PhasePending))
		Expect(refreshed.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeTargetFound))
		Expect(refreshed.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(refreshed.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonNotFound))
		Expect(refreshed.Status.Conditions[0].Message).To(Equal(msgTargetDeploymentNotExist))

		// Verify finalizer
		Expect(refreshed.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))
	})

	It("freezes and then unfreezes the Deployment, restoring replicas and clearing ownership", func() {
		By("creating the target Deployment")
		dep := makeDeployment(deployName, origReplicas, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ referencing the Deployment")
		dfz := makeDFZ(dfzName, deployName, 1)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		now := time.Now().UTC()
		r := newReconciler(now)

		// 1) First reconcile acquires ownership and scales spec to 0
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		// Verify status updated
		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())

		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(fmt.Sprintf(msgOwnershipAcquiredFmt, dfz.Name, dep.Namespace, dep.Name)))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgScalingDeploymentToZero))
		// Verify finalize
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))

		// 2) Second reconcile: Frozen phase
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFrozen))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(msgOwnershipAlreadyHeld)) // changed
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusTrue)) // changed
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScaledToZero))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgDeploymentFullyScaledToZero))
		Expect(curDFZ.Status.Conditions[2].Type).To(Equal(appsv1alpha1.ConditionTypeSpecChangedDuringFreeze))
		Expect(curDFZ.Status.Conditions[2].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[2].Reason).To(Equal(appsv1alpha1.ConditionReasonObserved))
		Expect(curDFZ.Status.Conditions[2].Message).To(Equal(msgSpecChangedDuringFreeze))

		// Now the Deployment should be scaled to 0 and owned by this DFZ
		var curDep appsv1.Deployment
		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &curDep)).To(Succeed())
		Expect(*curDep.Spec.Replicas).To(Equal(int32(0)))
		Expect(curDep.Annotations[annoFrozenBy]).To(Equal(fmt.Sprintf("%s/%s", ns, dfzName)))

		// 3) Advance time to trigger unfreeze path
		r.now = func() time.Time { return curDFZ.Status.FreezeUntil.Add(1 * time.Second).UTC() }

		// Transition to Unfreezing
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseUnfreezing))

		// 4) Final reconcile restores replicas and clears ownership
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseCompleted))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))    // changed
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonReleased)) // changed
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(msgOwnershipReleasedAfterUnfreeze))   // changed
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScaledToZero))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgDeploymentFullyScaledToZero))
		Expect(curDFZ.Status.Conditions[2].Type).To(Equal(appsv1alpha1.ConditionTypeSpecChangedDuringFreeze))
		Expect(curDFZ.Status.Conditions[2].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[2].Reason).To(Equal(appsv1alpha1.ConditionReasonObserved))
		Expect(curDFZ.Status.Conditions[2].Message).To(Equal(msgSpecChangedDuringFreeze))
		Expect(curDFZ.Status.Conditions[3].Type).To(Equal(appsv1alpha1.ConditionTypeUnfreezeProgress))
		Expect(curDFZ.Status.Conditions[3].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[3].Reason).To(Equal(appsv1alpha1.ConditionReasonScaledUp))
		Expect(curDFZ.Status.Conditions[3].Message).To(Equal(fmt.Sprintf(msgDeploymentRestoredReplicasFmt, origReplicas)))

		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &curDep)).To(Succeed())
		Expect(curDep.Spec.Replicas).NotTo(BeNil())
		Expect(*curDep.Spec.Replicas).To(Equal(origReplicas))
		Expect(curDep.Annotations[annoFrozenBy]).To(BeEmpty())
	})

	It("denies ownership if the Deployment is already frozen by another owner", func() {
		By("creating target Deployment already annotated as frozen by someone else")
		dep := makeDeployment(deployName, 1, map[string]string{annoFrozenBy: otherOwner})
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ that targets the same Deployment")
		dfz := makeDFZ(dfzName, deployName, 10)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		// DFZ should be Denied and Deployment annotation unchanged
		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseDenied))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonDeniedAlreadyFrozen))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(fmt.Sprintf(msgDeploymentAlreadyOwnedFmt, otherOwner)))
		// Verify finalize
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))

		var curDep appsv1.Deployment
		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &curDep)).To(Succeed())
		Expect(curDep.Annotations[annoFrozenBy]).To(Equal(otherOwner))
	})

	It("denies when spec.targetRef.name is empty", func() {
		By("creating DFZ with empty targetRef.name")
		dfz := makeDFZ(dfzName, "", 10)
		err := k8sClient.Create(ctx, dfz)
		Expect(err.Error()).To(Equal("DeploymentFreezer.apps.boolfixer.dev \"freeze-demo\" is invalid: spec.targetRef.name: Invalid value: \"\": spec.targetRef.name in body should be at least 1 chars long"))
	})

	It("stays Freezing while waiting for Deployment status to reach zero when spec is already zero", func() {
		By("creating the target Deployment with spec=0 but status showing non-zero")
		dep := makeDeployment(deployName, 0, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		// Simulate status not yet at zero
		var d appsv1.Deployment
		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &d)).To(Succeed())
		d.Status.Replicas = 1
		d.Status.ReadyReplicas = 1
		d.Status.AvailableReplicas = 1
		d.Status.UpdatedReplicas = 1
		Expect(k8sClient.Status().Update(ctx, &d)).To(Succeed())

		By("creating DFZ referencing the Deployment")
		dfz := makeDFZ(dfzName, deployName, 10)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		// Ownership condition set first
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		// Freeze progress indicates waiting for status to catch up
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgWaitingDeploymentReachZero))
		// finalizer ensured
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))
	})

	It("aborts when the Deployment is recreated with a different UID", func() {
		By("creating the original Deployment")
		dep := makeDeployment(deployName, 1, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ referencing the Deployment")
		dfz := makeDFZ(dfzName, deployName, 10)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		// First reconcile to record UID etc.
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		// Phase and conditions after first reconcile
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(fmt.Sprintf(msgOwnershipAcquiredFmt, dfz.Name, dep.Namespace, dep.Name)))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgScalingDeploymentToZero))
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))
		Expect(curDFZ.Status.TargetRef.UID).NotTo(BeEmpty())

		By("deleting the Deployment and creating a new one with the same name (different UID)")
		Expect(k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: deployName}})).To(Succeed())
		Expect(k8sClient.Create(ctx, makeDeployment(deployName, 1, nil))).To(Succeed())

		// Reconcile again should detect UID mismatch and abort
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseAborted))
		// Previously set conditions are retained
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		// This TargetFound condition is appended after existing conditions
		Expect(curDFZ.Status.Conditions[2].Type).To(Equal(appsv1alpha1.ConditionTypeTargetFound))
		Expect(curDFZ.Status.Conditions[2].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[2].Reason).To(Equal(appsv1alpha1.ConditionReasonUIDMismatch))
		Expect(curDFZ.Status.Conditions[2].Message).To(Equal(msgUIDRecreated))
	})

	It("aborts if ownership annotation is lost during Frozen phase", func() {
		By("creating the target Deployment")
		dep := makeDeployment(deployName, 1, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ referencing the Deployment with a future freeze window")
		dfz := makeDFZ(dfzName, deployName, 60)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		now := time.Now().UTC()
		r := newReconciler(now)

		// Drive to Frozen
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		// After first reconcile
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgScalingDeploymentToZero))
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))

		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFrozen))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(msgOwnershipAlreadyHeld))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScaledToZero))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgDeploymentFullyScaledToZero))
		Expect(curDFZ.Status.Conditions[2].Type).To(Equal(appsv1alpha1.ConditionTypeSpecChangedDuringFreeze))
		Expect(curDFZ.Status.Conditions[2].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[2].Reason).To(Equal(appsv1alpha1.ConditionReasonObserved))
		Expect(curDFZ.Status.Conditions[2].Message).To(Equal(msgSpecChangedDuringFreeze))

		By("simulating ownership loss on the Deployment")
		var curDep appsv1.Deployment
		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &curDep)).To(Succeed())
		if curDep.Annotations == nil {
			curDep.Annotations = map[string]string{}
		}
		curDep.Annotations[annoFrozenBy] = otherOwner // overwrite with different owner
		Expect(k8sClient.Update(ctx, &curDep)).To(Succeed())

		// Reconcile should detect ownership lost and abort
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseAborted))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonLost))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(msgOwnershipAnnotationLost))
	})

	It("releases replicas and clears ownership on DFZ deletion", func() {
		By("creating the target Deployment")
		dep := makeDeployment(deployName, 2, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ referencing the Deployment")
		dfz := makeDFZ(dfzName, deployName, 30)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		// First reconcile to acquire ownership and begin freezing
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(fmt.Sprintf(msgOwnershipAcquiredFmt, dfz.Name, dep.Namespace, dep.Name)))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgScalingDeploymentToZero))
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))

		By("deleting DFZ to trigger delete reconciliation path")
		Expect(k8sClient.Delete(ctx, dfz)).To(Succeed())

		// Run reconcile to process deletion (finalizer removal, best-effort restore and clear ownership)
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		// DFZ should be finalized and removed
		err = k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: dfzName}, &appsv1alpha1.DeploymentFreezer{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())

		// Deployment should have replicas restored and ownership cleared
		var curDep appsv1.Deployment
		Expect(get(types.NamespacedName{Namespace: ns, Name: deployName}, &curDep)).To(Succeed())
		Expect(curDep.Spec.Replicas).NotTo(BeNil())
		Expect(*curDep.Spec.Replicas).To(Equal(int32(2)))
		Expect(curDep.Annotations[annoFrozenBy]).To(BeEmpty())
	})

	It("moves to Aborted when target Deployment disappears mid-process", func() {
		By("creating the target Deployment")
		dep := makeDeployment(deployName, origReplicas, nil)
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())

		By("creating DFZ referencing the Deployment")
		dfz := makeDFZ(dfzName, deployName, 10)
		Expect(k8sClient.Create(ctx, dfz)).To(Succeed())

		r := newReconciler(time.Now().UTC())

		// First reconcile should set phase to Freezing (and set some conditions)
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		var curDFZ appsv1alpha1.DeploymentFreezer
		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseFreezing))
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[0].Message).To(Equal(fmt.Sprintf(msgOwnershipAcquiredFmt, dfz.Name, dep.Namespace, dep.Name)))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[1].Message).To(Equal(msgScalingDeploymentToZero))
		Expect(curDFZ.Finalizers).To(Equal([]string{"apps.boolfixer.dev/finalizer"}))

		// Delete the Deployment before next reconcile
		Expect(k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: deployName}})).To(Succeed())
		// Next reconcile: should set Phase Aborted and add TargetFound=false NotFound condition
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: dfzName}})
		Expect(err).NotTo(HaveOccurred())

		Expect(get(types.NamespacedName{Namespace: ns, Name: dfzName}, &curDFZ)).To(Succeed())
		Expect(curDFZ.Status.Phase).To(Equal(appsv1alpha1.PhaseAborted))
		// Retain previous conditions and append TargetFound
		Expect(curDFZ.Status.Conditions[0].Type).To(Equal(appsv1alpha1.ConditionTypeOwnership))
		Expect(curDFZ.Status.Conditions[0].Status).To(Equal(appsv1alpha1.ConditionStatusTrue))
		Expect(curDFZ.Status.Conditions[0].Reason).To(Equal(appsv1alpha1.ConditionReasonAcquired))
		Expect(curDFZ.Status.Conditions[1].Type).To(Equal(appsv1alpha1.ConditionTypeFreezeProgress))
		Expect(curDFZ.Status.Conditions[1].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[1].Reason).To(Equal(appsv1alpha1.ConditionReasonScalingDown))
		Expect(curDFZ.Status.Conditions[2].Type).To(Equal(appsv1alpha1.ConditionTypeTargetFound))
		Expect(curDFZ.Status.Conditions[2].Status).To(Equal(appsv1alpha1.ConditionStatusFalse))
		Expect(curDFZ.Status.Conditions[2].Reason).To(Equal(appsv1alpha1.ConditionReasonNotFound))
		Expect(curDFZ.Status.Conditions[2].Message).To(Equal(msgTargetDeploymentNotExist))
	})
})
