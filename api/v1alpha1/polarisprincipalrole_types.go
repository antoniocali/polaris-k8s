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

// PolarisPrincipalRoleSpec defines a server-wide role that can be assigned
// to principals.
type PolarisPrincipalRoleSpec struct {
	// connectionRef points at the PolarisConnection the role lives on.
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name is the role name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// properties is the open-ended property bag Polaris stores alongside the role.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisPrincipalRoleStatus reports the observed state of a principal role.
type PolarisPrincipalRoleStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pprole,categories=polaris
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisPrincipalRole is a server-wide role in Polaris that may be granted
// to one or more principals via PolarisPrincipalRoleBinding.
type PolarisPrincipalRole struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisPrincipalRoleSpec `json:"spec"`

	// +optional
	Status PolarisPrincipalRoleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisPrincipalRoleList contains a list of PolarisPrincipalRole.
type PolarisPrincipalRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisPrincipalRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisPrincipalRole{}, &PolarisPrincipalRoleList{})
}
