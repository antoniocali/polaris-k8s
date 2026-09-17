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

// PolarisViewSpec defines an Iceberg view managed in a Polaris namespace.
type PolarisViewSpec struct {
	// namespaceRef points at the PolarisNamespace this view lives in.
	NamespaceRef NamespaceRef `json:"namespaceRef"`

	// name is the view name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// schema is the output schema of the view.
	Schema IcebergSchema `json:"schema"`

	// sql is the view definition.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=65535
	SQL string `json:"sql"`

	// dialect identifies the SQL dialect the sql field is written in.
	// +optional
	// +kubebuilder:default:="spark"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Dialect string `json:"dialect,omitempty"`

	// properties is the open-ended view property bag Polaris stores
	// alongside the view.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisViewStatus reports the observed state of a view.
type PolarisViewStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// versionId is the integer version-id of the current view representation.
	// +optional
	VersionID int64 `json:"versionId,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pview,categories=polaris
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.namespaceRef.name`
// +kubebuilder:printcolumn:name="Dialect",type=string,JSONPath=`.spec.dialect`
// +kubebuilder:printcolumn:name="Version",type=integer,JSONPath=`.status.versionId`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisView is an Iceberg view managed inside a Polaris namespace.
type PolarisView struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisViewSpec `json:"spec"`

	// +optional
	Status PolarisViewStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisViewList contains a list of PolarisView.
type PolarisViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisView{}, &PolarisViewList{})
}
