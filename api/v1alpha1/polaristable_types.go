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

// WriteFormat enumerates the supported Iceberg data file formats.
// +kubebuilder:validation:Enum=parquet;orc;avro
type WriteFormat string

const (
	WriteFormatParquet WriteFormat = "parquet"
	WriteFormatORC     WriteFormat = "orc"
	WriteFormatAvro    WriteFormat = "avro"
)

// PolarisTableSpec defines an Iceberg table managed in a Polaris namespace.
type PolarisTableSpec struct {
	// namespaceRef points at the PolarisNamespace this table lives in.
	NamespaceRef NamespaceRef `json:"namespaceRef"`

	// name is the table name in Polaris. Defaults to .metadata.name when unset.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9_-]+$`
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// schema is the Iceberg schema for the table.
	Schema IcebergSchema `json:"schema"`

	// partitionSpec is the optional partitioning strategy.
	// +optional
	PartitionSpec *IcebergPartitionSpec `json:"partitionSpec,omitempty"`

	// writeFormat is the default data file format new writes will use.
	// +optional
	// +kubebuilder:default:=parquet
	WriteFormat WriteFormat `json:"writeFormat,omitempty"`

	// properties is the open-ended table property bag Polaris stores
	// alongside the table (e.g. write.target-file-size-bytes).
	// +optional
	Properties map[string]string `json:"properties,omitempty"`
}

// PolarisTableStatus reports the observed state of a table.
type PolarisTableStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// location is the resolved on-disk URI of the table.
	// +optional
	Location string `json:"location,omitempty"`

	// tableUuid is the stable Iceberg uuid of the table.
	// +optional
	TableUUID string `json:"tableUuid,omitempty"`

	// currentSnapshotId is the most recently committed snapshot, if any.
	// +optional
	CurrentSnapshotID int64 `json:"currentSnapshotId,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ptbl,categories=polaris
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.namespaceRef.name`
// +kubebuilder:printcolumn:name="Format",type=string,JSONPath=`.spec.writeFormat`
// +kubebuilder:printcolumn:name="Location",type=string,JSONPath=`.status.location`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// PolarisTable is an Iceberg table managed inside a Polaris namespace.
type PolarisTable struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec PolarisTableSpec `json:"spec"`

	// +optional
	Status PolarisTableStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PolarisTableList contains a list of PolarisTable.
type PolarisTableList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PolarisTable `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolarisTable{}, &PolarisTableList{})
}
