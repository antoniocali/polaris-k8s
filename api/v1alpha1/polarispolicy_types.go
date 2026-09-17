/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolarisPolicySpec defines a Polaris policy attached to a namespace
// (compaction, retention, etc.).
type PolarisPolicySpec struct {
	// namespaceRef points at the PolarisNamespace this policy applies to.
	NamespaceRef NamespaceRef `json:"namespaceRef"`

	// name is the policy name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_.-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// type identifies the Polaris policy type (e.g. system.data-compaction).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_.-]+$`
	Type string `json:"type"`

	// description is an optional human-readable description.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// content is the policy body. It is an arbitrary JSON object whose
	// schema is defined by the policy type. Polaris validates the contents
	// at apply time.
	Content apiextensionsv1.JSON `json:"content"`
}

// PolarisPolicyStatus reports the observed state of a policy.
type PolarisPolicyStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ppol,categories=polaris
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.namespaceRef.name`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisPolicy attaches a typed policy (e.g. compaction) to a Polaris namespace.
type PolarisPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisPolicySpec `json:"spec"`

	// +optional
	Status PolarisPolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisPolicyList contains a list of PolarisPolicy.
type PolarisPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisPolicy{}, &PolarisPolicyList{})
}
