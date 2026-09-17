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

// Privilege is the set of access rights Polaris recognises on a target.
// +kubebuilder:validation:Enum=CATALOG_MANAGE_CONTENT;CATALOG_MANAGE_ACCESS;CATALOG_MANAGE_METADATA;CATALOG_READ_PROPERTIES;CATALOG_WRITE_PROPERTIES;NAMESPACE_CREATE;NAMESPACE_DROP;NAMESPACE_LIST;NAMESPACE_READ_PROPERTIES;NAMESPACE_WRITE_PROPERTIES;NAMESPACE_FULL_METADATA;TABLE_CREATE;TABLE_DROP;TABLE_LIST;TABLE_READ_PROPERTIES;TABLE_WRITE_PROPERTIES;TABLE_READ_DATA;TABLE_WRITE_DATA;TABLE_FULL_METADATA;VIEW_CREATE;VIEW_DROP;VIEW_LIST;VIEW_READ_PROPERTIES;VIEW_WRITE_PROPERTIES;VIEW_FULL_METADATA
type Privilege string

// GrantTargetType selects what kind of Polaris resource a grant applies to.
// +kubebuilder:validation:Enum=catalog;namespace;table;view
type GrantTargetType string

const (
	GrantTargetCatalog   GrantTargetType = "catalog"
	GrantTargetNamespace GrantTargetType = "namespace"
	GrantTargetTable     GrantTargetType = "table"
	GrantTargetView      GrantTargetType = "view"
)

// GrantTarget identifies the resource a grant applies to. The required
// sub-reference depends on type; the cross-field rules are enforced on Spec.
type GrantTarget struct {
	// type selects the resource kind the privilege applies to.
	Type GrantTargetType `json:"type"`

	// namespaceRef is required when type is namespace, table, or view.
	// +optional
	NamespaceRef *NamespaceRef `json:"namespaceRef,omitempty"`

	// tableRef is required when type is table.
	// +optional
	TableRef *TableRef `json:"tableRef,omitempty"`

	// viewRef is required when type is view.
	// +optional
	ViewRef *ViewRef `json:"viewRef,omitempty"`
}

// PolarisGrantSpec attaches a single privilege on a single target to a
// PolarisCatalogRole.
// +kubebuilder:validation:XValidation:rule="self.target.type != 'namespace' || has(self.target.namespaceRef)",message="target.namespaceRef is required when target.type is namespace"
// +kubebuilder:validation:XValidation:rule="self.target.type != 'table' || (has(self.target.namespaceRef) && has(self.target.tableRef))",message="target.namespaceRef and target.tableRef are required when target.type is table"
// +kubebuilder:validation:XValidation:rule="self.target.type != 'view' || (has(self.target.namespaceRef) && has(self.target.viewRef))",message="target.namespaceRef and target.viewRef are required when target.type is view"
// +kubebuilder:validation:XValidation:rule="self.target.type != 'catalog' || (!has(self.target.namespaceRef) && !has(self.target.tableRef) && !has(self.target.viewRef))",message="when target.type is catalog, no sub-refs may be set"
type PolarisGrantSpec struct {
	// catalogRoleRef points at the PolarisCatalogRole receiving the grant.
	CatalogRoleRef CatalogRoleRef `json:"catalogRoleRef"`

	// privilege is the access right being granted.
	Privilege Privilege `json:"privilege"`

	// target identifies the Polaris resource the privilege applies to.
	Target GrantTarget `json:"target"`
}

// PolarisGrantStatus reports the observed state of a grant.
type PolarisGrantStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pgrant,categories=polaris
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=`.spec.catalogRoleRef.name`
// +kubebuilder:printcolumn:name="Privilege",type=string,JSONPath=`.spec.privilege`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisGrant attaches a Polaris privilege on a target (catalog, namespace,
// table, or view) to a PolarisCatalogRole. One privilege per CR — this keeps
// grants individually addressable for revocation and auditing.
type PolarisGrant struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisGrantSpec `json:"spec"`

	// +optional
	Status PolarisGrantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisGrantList contains a list of PolarisGrant.
type PolarisGrantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisGrant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisGrant{}, &PolarisGrantList{})
}
