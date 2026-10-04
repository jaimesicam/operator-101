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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// StaticSiteSpec is what the user asks for.
type StaticSiteSpec struct {
	// Content is the HTML served as index.html.
	// +kubebuilder:validation:MinLength=1
	Content string `json:"content"`

	// Replicas is how many nginx Pods to run.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// Image is the nginx image to run.
	// +kubebuilder:default="nginx:stable"
	// +optional
	Image string `json:"image,omitempty"`
}

// StaticSiteStatus is what the operator reports back.
type StaticSiteStatus struct {
	// State is a one-word summary: initializing, ready or error.
	// +optional
	State string `json:"state,omitempty"`

	// ReadyReplicas is how many nginx Pods are ready.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

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
// +kubebuilder:resource:shortName=site
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StaticSite is the Schema for the staticsites API.
type StaticSite struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of StaticSite
	// +required
	Spec StaticSiteSpec `json:"spec"`

	// status defines the observed state of StaticSite
	// +optional
	Status StaticSiteStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// StaticSiteList contains a list of StaticSite
type StaticSiteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []StaticSite `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &StaticSite{}, &StaticSiteList{})
		return nil
	})
}
