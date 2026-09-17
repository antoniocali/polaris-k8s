/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

// ConnectionRef points at a PolarisConnection. When Namespace is empty the
// referrer's own namespace is used at resolution time.
type ConnectionRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// CatalogRef points at a PolarisCatalog. Same-namespace by default.
type CatalogRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// NamespaceRef points at a PolarisNamespace. Same-namespace by default.
type NamespaceRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// TableRef points at a PolarisTable. Same-namespace by default.
type TableRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// ViewRef points at a PolarisView. Same-namespace by default.
type ViewRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// PrincipalRef points at a PolarisPrincipal. Same-namespace by default.
type PrincipalRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// PrincipalRoleRef points at a PolarisPrincipalRole. Same-namespace by default.
type PrincipalRoleRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// CatalogRoleRef points at a PolarisCatalogRole. Same-namespace by default.
type CatalogRoleRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// ClientCredentialsSecretRef points at a same-namespace Secret holding the
// OAuth client credentials used to authenticate to the Polaris server.
// The Secret must contain ClientIdKey and ClientSecretKey keys.
type ClientCredentialsSecretRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:default:="clientId"
	// +kubebuilder:validation:MinLength=1
	ClientIdKey string `json:"clientIdKey,omitempty"`

	// +optional
	// +kubebuilder:default:="clientSecret"
	// +kubebuilder:validation:MinLength=1
	ClientSecretKey string `json:"clientSecretKey,omitempty"`
}

// CABundleSecretRef points at a same-namespace Secret holding a PEM-encoded CA
// bundle used to validate the Polaris server certificate.
type CABundleSecretRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +optional
	// +kubebuilder:default:="ca.crt"
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key,omitempty"`
}

// GeneratedCredentialsSecretRef names the same-namespace Secret into which the
// operator will write the clientId/clientSecret returned by Polaris when the
// principal is created or rotated. The operator owns this Secret.
type GeneratedCredentialsSecretRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}
