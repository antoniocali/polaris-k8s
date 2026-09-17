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

// CatalogType enumerates the Polaris catalog provenance modes.
// +kubebuilder:validation:Enum=INTERNAL;EXTERNAL
type CatalogType string

const (
	CatalogTypeInternal CatalogType = "INTERNAL"
	CatalogTypeExternal CatalogType = "EXTERNAL"
)

// StorageType enumerates the cloud storage backends Polaris supports.
//
// FILE is local-filesystem storage that Polaris itself documents as "supported
// for testing purposes only". Polaris rejects FILE catalogs unless the server
// is started with ALLOW_INSECURE_STORAGE_TYPES=true, so it is safe to expose
// here for local/dev clusters without weakening production deployments.
// +kubebuilder:validation:Enum=S3;AZURE;GCS;FILE
type StorageType string

const (
	StorageTypeS3    StorageType = "S3"
	StorageTypeAzure StorageType = "AZURE"
	StorageTypeGCS   StorageType = "GCS"
	StorageTypeFile  StorageType = "FILE"
)

// S3StorageConfig holds AWS-specific storage configuration.
type S3StorageConfig struct {
	// roleArn is the IAM role Polaris will assume to access S3.
	// +kubebuilder:validation:Pattern=`^arn:aws[a-zA-Z-]*:iam::[0-9]{12}:role/.+$`
	RoleARN string `json:"roleArn"`

	// region is the AWS region of the bucket (e.g. eu-west-1).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Region string `json:"region"`

	// externalId, when set, is required on the role's trust policy.
	// +optional
	// +kubebuilder:validation:MaxLength=1224
	ExternalID string `json:"externalId,omitempty"`

	// userArn is an optional ARN of the user/role Polaris itself runs as,
	// surfaced for principals that need to add it to a trust policy.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	UserARN string `json:"userArn,omitempty"`
}

// AzureStorageConfig holds Azure-specific storage configuration.
type AzureStorageConfig struct {
	// tenantId is the Azure AD tenant id Polaris federates with.
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F-]{36}$`
	TenantID string `json:"tenantId"`

	// multiTenantAppName is the Azure AD multi-tenant application name, if used.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	MultiTenantAppName string `json:"multiTenantAppName,omitempty"`

	// consentUrl is the Azure consent URL surfaced to the operator for
	// admin consent flows.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	ConsentURL string `json:"consentUrl,omitempty"`
}

// GCSStorageConfig holds GCP-specific storage configuration.
type GCSStorageConfig struct {
	// gcsServiceAccount is the GCP service account email Polaris impersonates.
	// +kubebuilder:validation:Pattern=`^[^@]+@[^@]+\.iam\.gserviceaccount\.com$`
	GCSServiceAccount string `json:"gcsServiceAccount"`
}

// StorageConfig is the cloud-storage configuration backing a catalog.
// The s3 / azure / gcs sub-block matching storageType must be set; FILE
// storage (testing only) needs none, just allowedLocations.
// +kubebuilder:validation:XValidation:rule="self.storageType != 'S3' || has(self.s3)",message="s3 must be set when storageType is S3"
// +kubebuilder:validation:XValidation:rule="self.storageType != 'AZURE' || has(self.azure)",message="azure must be set when storageType is AZURE"
// +kubebuilder:validation:XValidation:rule="self.storageType != 'GCS' || has(self.gcs)",message="gcs must be set when storageType is GCS"
type StorageConfig struct {
	// storageType selects which cloud backend the catalog data lives in.
	StorageType StorageType `json:"storageType"`

	// allowedLocations enumerates the URI prefixes Polaris is permitted to
	// read from / write to. At least one is required.
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	AllowedLocations []string `json:"allowedLocations"`

	// +optional
	S3 *S3StorageConfig `json:"s3,omitempty"`

	// +optional
	Azure *AzureStorageConfig `json:"azure,omitempty"`

	// +optional
	GCS *GCSStorageConfig `json:"gcs,omitempty"`
}

// PolarisCatalogSpec defines a top-level catalog within a Polaris server.
type PolarisCatalogSpec struct {
	// connectionRef points at the PolarisConnection this catalog lives on.
	ConnectionRef ConnectionRef `json:"connectionRef"`

	// name is the catalog name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// type selects whether Polaris owns the catalog state (INTERNAL) or
	// federates from an external catalog (EXTERNAL).
	// +optional
	// +kubebuilder:default:=INTERNAL
	Type CatalogType `json:"type,omitempty"`

	// defaultBaseLocation is the storage URI under which new tables are
	// placed unless overridden.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	DefaultBaseLocation string `json:"defaultBaseLocation"`

	// storageConfig is the cloud-storage configuration backing the catalog.
	StorageConfig StorageConfig `json:"storageConfig"`

	// properties is the open-ended property bag Polaris associates with the
	// catalog. Keys/values are user-defined.
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisCatalogStatus reports the observed state of a catalog.
type PolarisCatalogStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// polarisCatalogId is the server-side identifier for the catalog.
	// +optional
	PolarisCatalogID string `json:"polarisCatalogId,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pcat,categories=polaris
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Storage",type=string,JSONPath=`.spec.storageConfig.storageType`
// +kubebuilder:printcolumn:name="Location",type=string,JSONPath=`.spec.defaultBaseLocation`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisCatalog is a top-level Polaris catalog (the parent of every Polaris
// namespace, table, view, and catalog-scoped role).
type PolarisCatalog struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisCatalogSpec `json:"spec"`

	// +optional
	Status PolarisCatalogStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisCatalogList contains a list of PolarisCatalog.
type PolarisCatalogList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisCatalog `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisCatalog{}, &PolarisCatalogList{})
}
