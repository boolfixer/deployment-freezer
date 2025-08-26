package controller

// Centralized catalog for condition messages to avoid drift and typos.

const (
	// General/validation/controller errors
	msgSpecTargetEmpty            = "spec.targetRef.name is empty"
	msgTargetDeploymentNotExist   = "Target Deployment does not exist"
	msgReadErrorFmt               = "read error: %v"
	msgUIDRecreated               = "Deployment was recreated with a different UID during the freeze lifecycle"
	msgTemplateHashPatchFailedFmt = "template hash patch failed: %v"

	// Ownership related
	msgDeploymentAlreadyOwnedFmt      = "Deployment is already owned by %s"
	msgOwnershipAcquiredFmt           = "DFZ %s owns Deployment %s/%s"
	msgOwnershipAlreadyHeld           = "Ownership already held"
	msgOwnershipAnnotationLost        = "Ownership annotation disappeared or was overwritten"
	msgOwnershipReleasedAfterUnfreeze = "Ownership released after unfreeze"

	// Freeze progress related
	msgCannotScaleDownYetFmt       = "cannot scale down yet: %v"
	msgScalingDeploymentToZero     = "Scaling Deployment to 0"
	msgDeploymentFullyScaledToZero = "Deployment is fully scaled to zero"
	msgWaitingDeploymentReachZero  = "Waiting for Deployment to reach zero replicas"

	// Unfreeze related
	msgFailedRestoreReplicasFmt      = "failed to restore replicas to %d: %v"
	msgFailedClearOwnershipFmt       = "failed to clear ownership: %v"
	msgDeploymentRestoredReplicasFmt = "Deployment restored to %d replicas"

	// Spec change detection
	msgSpecChangedDuringFreeze = "Target Deployment's pod template changed during the lifecycle"
)
