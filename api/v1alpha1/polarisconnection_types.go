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

// PolarisConnectionSpec defines the desired state of a connection to an
// Apache Polaris REST catalog server.
type PolarisConnectionSpec struct {
	// serverUrl is the base URL of the Polaris server, e.g. https://polaris.internal.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://.+`
	ServerURL string `json:"serverUrl"`

	// tokenPath is the OAuth token endpoint path appended to serverUrl. Defaults
	// to the standard Polaris token endpoint.
	// +optional
	// +kubebuilder:default:="/api/catalog/v1/oauth/tokens"
	// +kubebuilder:validation:MinLength=1
	TokenPath string `json:"tokenPath,omitempty"`

	// scope is the OAuth scope requested when minting tokens. Defaults to
	// PRINCIPAL_ROLE:ALL which gives the operator full management access.
	// +optional
	// +kubebuilder:default:="PRINCIPAL_ROLE:ALL"
	// +kubebuilder:validation:MinLength=1
	Scope string `json:"scope,omitempty"`

	// credentialsSecretRef points at a Secret containing the OAuth clientId and
	// clientSecret used to authenticate to Polaris.
	CredentialsSecretRef ClientCredentialsSecretRef `json:"credentialsSecretRef"`

	// caBundleSecretRef optionally references a Secret containing a PEM CA
	// bundle for validating the Polaris server certificate. Required for
	// self-signed deployments.
	// +optional
	CABundleSecretRef *CABundleSecretRef `json:"caBundleSecretRef,omitempty"`

	// insecureSkipVerify disables TLS verification for the Polaris server.
	// Strongly discouraged outside of local development.
	// +optional
	// +kubebuilder:default:=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// PolarisConnectionStatus reports the observed state of a connection.
type PolarisConnectionStatus struct {
	// observedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// serverVersion is the version reported by the Polaris server, if
	// discoverable.
	// +optional
	ServerVersion string `json:"serverVersion,omitempty"`

	// conditions represent the current state of the connection.
	// Standard types: Ready, AuthValid.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pconn,categories=polaris
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverUrl`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisConnection is a handle to an Apache Polaris REST catalog server.
// Every other resource in this API group attaches to a PolarisConnection
// either directly or via a parent resource that does.
type PolarisConnection struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisConnectionSpec `json:"spec"`

	// +optional
	Status PolarisConnectionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisConnectionList contains a list of PolarisConnection.
type PolarisConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisConnection{}, &PolarisConnectionList{})
}
