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

// PolarisCatalogRoleSpec defines a role scoped to a specific Polaris catalog.
// PolarisGrant resources attach privileges to a CatalogRole; bindings then
// connect principal roles to catalog roles.
type PolarisCatalogRoleSpec struct {
	// catalogRef points at the PolarisCatalog this role is scoped to.
	CatalogRef CatalogRef `json:"catalogRef"`

	// name is the role name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// properties is the open-ended property bag Polaris stores alongside the role.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisCatalogRoleStatus reports the observed state of a catalog role.
type PolarisCatalogRoleStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pcrole,categories=polaris
// +kubebuilder:printcolumn:name="Catalog",type=string,JSONPath=`.spec.catalogRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisCatalogRole is a role scoped to a single Polaris catalog. Privileges
// are attached via PolarisGrant; principals reach this role via a
// PolarisCatalogRoleBinding from a PolarisPrincipalRole.
type PolarisCatalogRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisCatalogRoleSpec `json:"spec"`

	// +optional
	Status PolarisCatalogRoleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisCatalogRoleList contains a list of PolarisCatalogRole.
type PolarisCatalogRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisCatalogRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisCatalogRole{}, &PolarisCatalogRoleList{})
}
