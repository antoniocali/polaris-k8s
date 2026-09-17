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

// PolarisPrincipalSpec defines an identity (user or service) in Polaris.
type PolarisPrincipalSpec struct {
	// connectionRef points at the PolarisConnection the principal lives on.
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name is the principal name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// credentialRotationRequired marks the principal as needing its
	// clientSecret rotated on next reconcile.
	// +optional
	// +kubebuilder:default:=false
	CredentialRotationRequired bool `json:"credentialRotationRequired,omitempty"`

	// properties is the open-ended property bag Polaris stores alongside
	// the principal (e.g. department, owner-team).
	// +optional
	Properties map[string]string `json:"properties,omitempty"`

	// credentialsSecretRef names the same-namespace Secret into which the
	// operator writes the clientId and clientSecret returned by Polaris when
	// the principal is created or rotated. The operator owns this Secret.
	CredentialsSecretRef GeneratedCredentialsSecretRef `json:"credentialsSecretRef"`
}

// PolarisPrincipalStatus reports the observed state of a principal.
type PolarisPrincipalStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// principalId is the server-side identifier for the principal.
	// +optional
	PrincipalID string `json:"principalId,omitempty"`

	// credentialsLastRotated records when the operator last wrote a new
	// clientSecret into credentialsSecretRef.
	// +optional
	CredentialsLastRotated *metav1.Time `json:"credentialsLastRotated,omitempty"`

	// conditions represent the current state of the principal.
	// Standard types: Ready, CredentialsWritten.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pprin,categories=polaris
// +kubebuilder:printcolumn:name="PrincipalID",type=string,JSONPath=`.status.principalId`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.spec.credentialsSecretRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisPrincipal is an identity (user or service) registered in Polaris.
// The operator manages the lifecycle of the principal and surfaces the
// generated clientId/clientSecret to a user-named Secret.
type PolarisPrincipal struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisPrincipalSpec `json:"spec"`

	// +optional
	Status PolarisPrincipalStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisPrincipalList contains a list of PolarisPrincipal.
type PolarisPrincipalList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisPrincipal `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisPrincipal{}, &PolarisPrincipalList{})
}
