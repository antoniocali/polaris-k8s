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

// PolarisCatalogRoleBindingSpec binds a PolarisPrincipalRole to a
// PolarisCatalogRole so that holders of the principal role inherit the
// catalog-scoped grants.
type PolarisCatalogRoleBindingSpec struct {
	// principalRoleRef points at the PolarisPrincipalRole granted access.
	PrincipalRoleRef PrincipalRoleRef `json:"principalRoleRef"`

	// catalogRoleRef points at the PolarisCatalogRole being attached.
	CatalogRoleRef CatalogRoleRef `json:"catalogRoleRef"`
}

// PolarisCatalogRoleBindingStatus reports the observed state of a binding.
type PolarisCatalogRoleBindingStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pcrb,categories=polaris
// +kubebuilder:printcolumn:name="PrincipalRole",type=string,JSONPath=`.spec.principalRoleRef.name`
// +kubebuilder:printcolumn:name="CatalogRole",type=string,JSONPath=`.spec.catalogRoleRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisCatalogRoleBinding grants a PolarisCatalogRole to a
// PolarisPrincipalRole — the bridge that lets a principal role's holders
// inherit catalog-scoped privileges.
type PolarisCatalogRoleBinding struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisCatalogRoleBindingSpec `json:"spec"`

	// +optional
	Status PolarisCatalogRoleBindingStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisCatalogRoleBindingList contains a list of PolarisCatalogRoleBinding.
type PolarisCatalogRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisCatalogRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisCatalogRoleBinding{}, &PolarisCatalogRoleBindingList{})
}
