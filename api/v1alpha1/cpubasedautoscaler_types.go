/*
Copyright 2025 vshulcz.

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
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

type TargetReference struct {
	// +kubebuilder:validation:MinLength=1
	APIVersion string `json:"apiVersion"`

	// +kubebuilder:validation:Enum=Deployment;StatefulSet
	Kind string `json:"kind"`

	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type SourceType string

const (
	SourceGolectra   SourceType = "golectra"
	SourcePrometheus SourceType = "prometheus"
)

type GolectraSource struct {
	// http://golectra.default.svc:8080
	// +kubebuilder:validation:MinLength=1
	BaseURL string `json:"baseURL"`

	// +kubebuilder:default="/api/v1/snapshot"
	// +kubebuilder:validation:MinLength=1
	SnapshotPath string `json:"snapshotPath,omitempty"`

	// +kubebuilder:default=800
	// +kubebuilder:validation:Minimum=1
	TimeoutMs int32 `json:"timeoutMs,omitempty"`

	// +kubebuilder:default="CPUutilization"
	// +kubebuilder:validation:MinLength=1
	CPUKeyPrefix string `json:"cpuKeyPrefix,omitempty"`
}

type SourceSpec struct {
	// Тип источника метрик.
	// +kubebuilder:validation:Enum=golectra;prometheus
	Type SourceType `json:"type"`

	// Настройки Golectra.
	// +optional
	Golectra *GolectraSource `json:"golectra,omitempty"`
}

// +kubebuilder:validation:Enum=reactive
type ControlMode string

const (
	ControlModeReactive ControlMode = "reactive"
)

type ControlSpec struct {
	// +kubebuilder:default=reactive
	Mode ControlMode `json:"mode,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	TargetCPUPercent int32 `json:"targetCPUPercent"`

	// +kubebuilder:validation:ExclusiveMinimum=true
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +kubebuilder:default=0.4
	Alpha float64 `json:"alpha,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=3
	WarmupPoints int32 `json:"warmupPoints,omitempty"`

	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=15
	DTSeconds int32 `json:"dtSeconds,omitempty"`
}

type PolicySpec struct {
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	MinReplicas int32 `json:"minReplicas,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=50
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=5
	MaxStepUp int32 `json:"maxStepUp,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3
	MaxStepDown int32 `json:"maxStepDown,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=60
	StabilizationWindowSec int32 `json:"stabilizationWindowSec,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=60
	ErrorFreezeSeconds int32 `json:"errorFreezeSeconds,omitempty"`
}

// CPUBasedAutoscalerSpec defines the desired state of CPUBasedAutoscaler
type CPUBasedAutoscalerSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	TargetRef TargetReference `json:"targetRef"`
	Source    SourceSpec      `json:"source"`
	Control   ControlSpec     `json:"control"`
	Policy    PolicySpec      `json:"policy"`
}

type DecisionReason string

const (
	ReasonForecast   DecisionReason = "forecast"
	ReasonStabilized DecisionReason = "stabilized"
	ReasonMaxStep    DecisionReason = "max_step"
	ReasonFreeze     DecisionReason = "freeze"
)

// CPUBasedAutoscalerStatus defines the observed state of CPUBasedAutoscaler.
type CPUBasedAutoscalerStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the CPUBasedAutoscaler resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	LastRun *metav1.Time `json:"lastRun,omitempty"`

	// +optional
	LastError string `json:"lastError,omitempty"`

	// +optional
	CPUNowPercent    float64 `json:"cpuNowPercent,omitempty"`
	CPUSmoothPercent float64 `json:"cpuSmoothPercent,omitempty"`

	// +optional
	CurrentReplicas int32 `json:"currentReplicas,omitempty"`
	DesiredReplicas int32 `json:"desiredReplicas,omitempty"`
	AppliedReplicas int32 `json:"appliedReplicas,omitempty"`

	// +optional
	Reason DecisionReason `json:"reason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cpuba,categories=autoscaling
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetRef.kind`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.targetRef.name`
// +kubebuilder:printcolumn:name="CPU(smooth)%",type=string,JSONPath=`.status.cpuSmoothPercent`,priority=1
// +kubebuilder:printcolumn:name="Cur",type=integer,JSONPath=`.status.currentReplicas`
// +kubebuilder:printcolumn:name="Des",type=integer,JSONPath=`.status.desiredReplicas`,priority=1
// +kubebuilder:printcolumn:name="App",type=integer,JSONPath=`.status.appliedReplicas`,priority=1
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.reason`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
//
/*
+kubebuilder:validation:XValidation:rule="self.policy.maxReplicas >= self.policy.minReplicas",message="maxReplicas must be >= minReplicas"
+kubebuilder:validation:XValidation:rule="self.source.type == 'golectra' ? has(self.source.golectra) : true",message="source.golectra must be set for type=golectra"
+kubebuilder:validation:XValidation:rule="self.source.type == 'prometheus' ? has(self.source.prometheus) : true",message="source.prometheus must be set for type=prometheus"
*/

// CPUBasedAutoscaler is the Schema for the cpubasedautoscalers API
type CPUBasedAutoscaler struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of CPUBasedAutoscaler
	// +required
	Spec CPUBasedAutoscalerSpec `json:"spec"`

	// status defines the observed state of CPUBasedAutoscaler
	// +optional
	Status CPUBasedAutoscalerStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CPUBasedAutoscalerList contains a list of CPUBasedAutoscaler
type CPUBasedAutoscalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CPUBasedAutoscaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CPUBasedAutoscaler{}, &CPUBasedAutoscalerList{})
}
