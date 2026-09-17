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

// PolarisNamespaceSpec defines a namespace within a Polaris catalog.
// Nesting is expressed by chaining parentRef rather than by carrying the full
// path on a single CR — this way each level has its own lifecycle, ownerRef
// chain, and reconciliation status.
type PolarisNamespaceSpec struct {
	// catalogRef points at the PolarisCatalog this namespace lives under.
	CatalogRef CatalogRef `json:"catalogRef"`

	// parentRef, when set, makes this a nested namespace under another
	// PolarisNamespace. When unset the namespace is a top-level child of the
	// catalog. Both refs must resolve to the same catalog at reconcile time.
	// +optional
	ParentRef *NamespaceRef `json:"parentRef,omitempty"`

	// name is the final path segment of the namespace in Polaris. Defaults to
	// .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// properties is the open-ended property bag Polaris associates with the
	// namespace. Common keys: location, owner.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisNamespaceStatus reports the observed state of a namespace.
type PolarisNamespaceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// fullPath is the fully qualified namespace path as Polaris sees it,
	// e.g. ["analytics", "sales"]. Resolved by walking parentRef.
	// +optional
	// +listType=atomic
	FullPath []string `json:"fullPath,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pns,categories=polaris
// +kubebuilder:printcolumn:name="Catalog",type=string,JSONPath=`.spec.catalogRef.name`
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.status.fullPath`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisNamespace is a logical container for tables and views inside a
// Polaris catalog. Namespaces may be nested via parentRef.
type PolarisNamespace struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisNamespaceSpec `json:"spec"`

	// +optional
	Status PolarisNamespaceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisNamespaceList contains a list of PolarisNamespace.
type PolarisNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisNamespace `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisNamespace{}, &PolarisNamespaceList{})
}
