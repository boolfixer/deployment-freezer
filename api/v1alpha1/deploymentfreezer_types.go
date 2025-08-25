/*
Copyright 2025 boolfixer.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type DeploymentTargetRef struct {
	// Name of the target Deployment (same namespace as this CR).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

type DeploymentFreezerSpec struct {
	// Target Deployment reference.
	TargetRef DeploymentTargetRef `json:"targetRef"`

	// Duration of the freeze window in seconds. After this period, the operator restores the Deployment.
	// +kubebuilder:validation:Minimum=1
	DurationSeconds int64 `json:"durationSeconds"`
}

type Phase string

const (
	PhasePending    Phase = "Pending"
	PhaseFreezing   Phase = "Freezing"
	PhaseFrozen     Phase = "Frozen"
	PhaseUnfreezing Phase = "Unfreezing"
	PhaseCompleted  Phase = "Completed"
	PhaseDenied     Phase = "Denied"
	PhaseAborted    Phase = "Aborted"
)

// +kubebuilder:validation:Enum=Pending;Freezing;Frozen;Unfreezing;Completed;Denied;Aborted
// Phase summarises the lifecycle of a DeploymentFreezer.
type _PhaseEnumValidationHolder struct{}

type ConditionType string

const (
	ConditionTypeTargetFound             ConditionType = "TargetFound"
	ConditionTypeOwnership               ConditionType = "Ownership"
	ConditionTypeFreezeProgress          ConditionType = "FreezeProgress"
	ConditionTypeUnfreezeProgress        ConditionType = "UnfreezeProgress"
	ConditionTypeHealth                  ConditionType = "Health"
	ConditionTypeSpecChangedDuringFreeze ConditionType = "SpecChangedDuringFreeze"
)

// +kubebuilder:validation:Enum=TargetFound;Ownership;FreezeProgress;UnfreezeProgress;Health;SpecChangedDuringFreeze
type _ConditionTypeEnumValidationHolder struct{}

type ConditionStatus string

const (
	ConditionStatusTrue    ConditionStatus = "True"
	ConditionStatusFalse   ConditionStatus = "False"
	ConditionStatusUnknown ConditionStatus = "Unknown"
)

// +kubebuilder:validation:Enum=True;False;Unknown
type _ConditionStatusEnumValidationHolder struct{}

type ConditionReason string

const (
	// TargetFound reasons
	ConditionReasonFound       ConditionReason = "Found"
	ConditionReasonNotFound    ConditionReason = "NotFound"
	ConditionReasonUIDMismatch ConditionReason = "UIDMismatch"

	// Ownership reasons
	ConditionReasonAcquired            ConditionReason = "Acquired"
	ConditionReasonDeniedAlreadyFrozen ConditionReason = "DeniedAlreadyFrozen"
	ConditionReasonLost                ConditionReason = "Lost"
	ConditionReasonReleased            ConditionReason = "Released"

	// FreezeProgress reasons
	ConditionReasonScalingDown  ConditionReason = "ScalingDown"
	ConditionReasonScaledToZero ConditionReason = "ScaledToZero"
	ConditionReasonAwaitingPDB  ConditionReason = "AwaitingPDB"

	// UnfreezeProgress reasons
	ConditionReasonScalingUp      ConditionReason = "ScalingUp"
	ConditionReasonScaledUp       ConditionReason = "ScaledUp"
	ConditionReasonQuotaExceeded  ConditionReason = "QuotaExceeded"
	ConditionReasonPartialRestore ConditionReason = "PartialRestore"

	// Health reasons
	ConditionReasonNormal      ConditionReason = "Normal"
	ConditionReasonDegraded    ConditionReason = "Degraded"
	ConditionReasonAPIConflict ConditionReason = "APIConflict"
	ConditionReasonRBACDenied  ConditionReason = "RBACDenied"

	// SpecChangedDuringFreeze reasons
	ConditionReasonObserved ConditionReason = "Observed"
)

// +kubebuilder:validation:Enum=Found;NotFound;UIDMismatch;Acquired;DeniedAlreadyFrozen;Lost;Released;ScalingDown;ScaledToZero;AwaitingPDB;ScalingUp;ScaledUp;QuotaExceeded;PartialRestore;Normal;Degraded;APIConflict;RBACDenied;Observed
type _ConditionReasonEnumValidationHolder struct{}

type StatusTargetRef struct {
	// Cached name of the target Deployment.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name,omitempty"`

	// UID of the Deployment at the time the freeze began
	// (detects delete+recreate under the same name).
	UID types.UID `json:"uid,omitempty"`
}

type Condition struct {
	// Category of fact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TargetFound;Ownership;FreezeProgress;UnfreezeProgress;Health;SpecChangedDuringFreeze
	Type ConditionType `json:"type"`

	// Whether the condition is satisfied.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=True;False;Unknown
	Status ConditionStatus `json:"status"`

	// Short CamelCase reason for the last transition.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Found;NotFound;UIDMismatch;Acquired;DeniedAlreadyFrozen;Lost;Released;ScalingDown;ScaledToZero;AwaitingPDB;ScalingUp;ScaledUp;QuotaExceeded;PartialRestore;Normal;Degraded;APIConflict;RBACDenied;Observed
	Reason ConditionReason `json:"reason,omitempty"`

	// Human-readable message (for operators/users).
	// +kubebuilder:validation:MaxLength=2048
	Message string `json:"message,omitempty"`

	// RFC3339 time of the last status change.
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

type DeploymentFreezerStatus struct {
	// High-level lifecycle summary.
	// +kubebuilder:validation:Enum=Pending;Freezing;Frozen;Unfreezing;Completed;Denied;Aborted
	Phase Phase `json:"phase,omitempty"`

	// Last observed generation of the CR's spec.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Cached target info recorded when the freeze started.
	TargetRef StatusTargetRef `json:"targetRef,omitempty"`

	// Replicas before freezing (for deterministic restore).
	OriginalReplicas *int32 `json:"originalReplicas,omitempty"`

	// Absolute time when the Deployment should be unfrozen.
	FreezeUntil *metav1.Time `json:"freezeUntil,omitempty"`

	// Fine-grained condition set.
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories=all,shortName=df
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="FreezeUntil",type=string,JSONPath=`.status.freezeUntil`
type DeploymentFreezer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeploymentFreezerSpec   `json:"spec,omitempty"`
	Status DeploymentFreezerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type DeploymentFreezerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeploymentFreezer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeploymentFreezer{}, &DeploymentFreezerList{})
}
