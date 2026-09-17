/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolarisPrincipalRoleBindingSpec binds a PolarisPrincipal to a
// PolarisPrincipalRole. Each binding is one assignment; many-to-many
// relationships are expressed as multiple CRs.
type PolarisPrincipalRoleBindingSpec struct {
	// principalRef points at the PolarisPrincipal being granted the role.
	PrincipalRef PrincipalRef `json:"principalRef"`

	// principalRoleRef points at the PolarisPrincipalRole being assigned.
	PrincipalRoleRef PrincipalRoleRef `json:"principalRoleRef"`
}

// PolarisPrincipalRoleBindingStatus reports the observed state of a binding.
type PolarisPrincipalRoleBindingStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pprb,categories=polaris
// +kubebuilder:printcolumn:name="Principal",type=string,JSONPath=`.spec.principalRef.name`
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=`.spec.principalRoleRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisPrincipalRoleBinding grants a PolarisPrincipalRole to a
// PolarisPrincipal. The binding can be removed independently of either side.
type PolarisPrincipalRoleBinding struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisPrincipalRoleBindingSpec `json:"spec"`

	// +optional
	Status PolarisPrincipalRoleBindingStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisPrincipalRoleBindingList contains a list of PolarisPrincipalRoleBinding.
type PolarisPrincipalRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisPrincipalRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisPrincipalRoleBinding{}, &PolarisPrincipalRoleBindingList{})
}
