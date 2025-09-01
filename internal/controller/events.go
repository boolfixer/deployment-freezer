package controller

const (
	ReasonOwnershipDenied      = "OwnershipDenied"
	ReasonFrozen               = "Frozen"
	ReasonOwnershipLost        = "OwnershipLost"
	ReasonUnfreezingStarted    = "UnfreezingStarted"
	ReasonUnfreezeCompleted    = "UnfreezeCompleted"
	ReasonSkippedNotOwner      = "SkippedNotOwner"
	ReasonRestoreFailed        = "RestoreReplicasFailed"
	ReasonRestored             = "ReplicasRestored"
	ReasonClearOwnershipFailed = "ClearOwnershipFailed"
	ReasonOwnershipCleared     = "OwnershipCleared"
)

const (
	msgOwnershipDenied       = "Deployment %s/%s is already owned by %s"
	msgFrozenUntil           = "Deployment frozen until %s"
	msgOwnershipLost         = "Ownership annotation lost or overwritten on Deployment %s/%s"
	msgUnfreezingStarted     = "Freeze window elapsed; starting unfreeze"
	msgUnfreezeCompleted     = "Unfreeze completed; replicas restored to %d"
	msgSkippedNotOwner       = "Ownership annotation does not match; expected %q"
	msgReplicasRestoreFailed = "Failed to restore replicas to %d: %v"
	msgReplicasRestored      = "Restored replicas to %d"
	msgClearOwnershipFailed  = "Failed to clear ownership annotation: %v"
	msgOwnershipCleared      = "Cleared ownership annotation on Deployment %s/%s"
)
