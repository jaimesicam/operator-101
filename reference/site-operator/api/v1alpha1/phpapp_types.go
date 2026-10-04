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

// ComponentSpec is shared by the php and nginx parts.
type ComponentSpec struct {
	// Image overrides the default image for this part.
	// +kubebuilder:validation:XValidation:rule="!self.endsWith(':latest')",message="pin a version; the :latest tag is not allowed"
	// +optional
	Image string `json:"image,omitempty"`

	// Replicas is how many Pods to run for this part.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=1
	// +optional
	Replicas int32 `json:"replicas,omitempty"`
}

// PhpAppSpec is what the user asks for.
type PhpAppSpec struct {
	// Code is the content of index.php.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self.contains('<?php')",message="code must contain an opening <?php tag"
	Code string `json:"code"`

	// SecretName names a Secret whose keys become env vars for PHP.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="secretName cannot be changed; create a new PhpApp instead"
	SecretName string `json:"secretName"`

	// Php configures the php-fpm part.
	// +kubebuilder:default={}
	// +optional
	Php ComponentSpec `json:"php,omitempty"`

	// Nginx configures the nginx part.
	// +kubebuilder:default={}
	// +optional
	Nginx ComponentSpec `json:"nginx,omitempty"`
}

// PhpAppStatus is what the operator reports back.
type PhpAppStatus struct {
	// State is a one-word summary: initializing, ready or error.
	// +optional
	State string `json:"state,omitempty"`

	// ObservedGeneration is the spec generation this status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions are the detailed checklist (SecretFound, PhpReady, Ready).
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=php
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PhpApp is the Schema for the phpapps API.
type PhpApp struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PhpApp
	// +required
	Spec PhpAppSpec `json:"spec"`

	// status defines the observed state of PhpApp
	// +optional
	Status PhpAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PhpAppList contains a list of PhpApp
type PhpAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PhpApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PhpApp{}, &PhpAppList{})
		return nil
	})
}
