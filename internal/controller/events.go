package controller

const (
	ReasonOwnershipDenied             = "OwnershipDenied"
	ReasonFrozen                      = "Frozen"
	ReasonOwnershipLost               = "OwnershipLost"
	ReasonUnfreezingStarted           = "UnfreezingStarted"
	ReasonUnfreezeCompleted           = "UnfreezeCompleted"
	ReasonReleaseSkippedNoTarget      = "ReleaseSkippedNoTarget"
	ReasonReleaseSkippedNotFound      = "ReleaseSkippedNotFound"
	ReasonReleaseSkippedNotOwner      = "ReleaseSkippedNotOwner"
	ReasonReleaseRestoreFailed        = "ReleaseRestoreReplicasFailed"
	ReasonReleaseRestored             = "ReleaseReplicasRestored"
	ReasonReleaseClearOwnershipFailed = "ReleaseClearOwnershipFailed"
	ReasonReleaseOwnershipCleared     = "ReleaseOwnershipCleared"
)

const (
	msgEvtOwnershipDenied             = "Deployment %s/%s is already owned by %s"
	msgEvtFrozenUntil                 = "Deployment frozen until %s"
	msgEvtOwnershipLost               = "Ownership annotation lost or overwritten on Deployment %s/%s"
	msgEvtUnfreezingStarted           = "Freeze window elapsed; starting unfreeze"
	msgEvtUnfreezeCompleted           = "Unfreeze completed; replicas restored to %d"
	msgEvtReleaseSkippedNoTarget      = "No targetRef.name specified; nothing to release"
	msgEvtReleaseSkippedNotFound      = "Target Deployment %s/%s not found"
	msgEvtReleaseSkippedNotOwner      = "Ownership annotation does not match; expected %q"
	msgEvtReleaseRestoreFailed        = "Failed to restore replicas to %d: %v"
	msgEvtReleaseRestored             = "Restored replicas to %d"
	msgEvtReleaseClearOwnershipFailed = "Failed to clear ownership annotation: %v"
	msgEvtReleaseOwnershipCleared     = "Cleared ownership annotation on Deployment %s/%s"
)
