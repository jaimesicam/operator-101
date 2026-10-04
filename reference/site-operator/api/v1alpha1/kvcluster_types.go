/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// KVClusterSpec is what the user asks for.
type KVClusterSpec struct {
	// Replicas is the number of members: one primary, the rest replicas.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	// +kubebuilder:default=3
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Storage is the disk size for each member. It can grow but never shrink.
	// +kubebuilder:default="1Gi"
	// +kubebuilder:validation:XValidation:rule="quantity(string(self)).compareTo(quantity(string(oldSelf))) >= 0",message="storage can only grow"
	// +optional
	Storage resource.Quantity `json:"storage,omitempty"`

	// StorageClassName picks the disk type. Empty means the cluster default.
	// Fixed at creation time.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="storageClassName cannot be changed"
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// Image overrides the default Valkey image.
	// +optional
	Image string `json:"image,omitempty"`
}

// KVClusterStatus is what the operator reports back.
type KVClusterStatus struct {
	// State is a one-word summary: initializing or ready.
	// +optional
	State string `json:"state,omitempty"`

	// ReadyReplicas is how many members are ready.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Primary is the Pod currently taking writes.
	// +optional
	Primary string `json:"primary,omitempty"`

	// ObservedGeneration is the spec generation this status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions are the detailed checklist (Ready, ...).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=kv
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Primary",type=string,JSONPath=`.status.primary`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// KVCluster is the Schema for the kvclusters API.
type KVCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of KVCluster
	// +required
	Spec KVClusterSpec `json:"spec"`

	// status defines the observed state of KVCluster
	// +optional
	Status KVClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// KVClusterList contains a list of KVCluster
type KVClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []KVCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &KVCluster{}, &KVClusterList{})
		return nil
	})
}
