/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

// IcebergSchema is the structural schema of a Polaris/Iceberg table or view.
type IcebergSchema struct {
	// fields are the columns of the schema, in declaration order.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10000
	Fields []IcebergField `json:"fields"`

	// identifierFieldIds carries the primary-key field ids, if any.
	// +optional
	// +listType=set
	IdentifierFieldIds []int32 `json:"identifierFieldIds,omitempty"`
}

// IcebergField describes a single column in an Iceberg schema.
type IcebergField struct {
	// id is the Iceberg field id (stable across schema evolution).
	// +kubebuilder:validation:Minimum=1
	ID int32 `json:"id"`

	// name is the column name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// type is the Iceberg type (e.g. "int", "long", "string", "decimal(10,2)",
	// or a nested "struct<...>"). Validation is intentionally loose — Polaris
	// itself is the source of truth for valid types.
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// required indicates whether the field is NOT NULL. Defaults to false.
	// +optional
	// +kubebuilder:default:=false
	Required bool `json:"required,omitempty"`

	// doc is an optional human-readable description of the field.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Doc string `json:"doc,omitempty"`
}

// IcebergPartitionSpec describes the partitioning strategy of a table.
type IcebergPartitionSpec struct {
	// fields are the partition transforms applied to source columns.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=100
	Fields []IcebergPartitionField `json:"fields"`
}

// IcebergPartitionField is a single partition column produced by applying
// a transform to a source schema field.
type IcebergPartitionField struct {
	// sourceId is the id of the source schema field this partition derives from.
	// +kubebuilder:validation:Minimum=1
	SourceID int32 `json:"sourceId"`

	// fieldId is the id of the partition field itself.
	// +kubebuilder:validation:Minimum=1
	FieldID int32 `json:"fieldId"`

	// name is the partition column name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// transform is the Iceberg partition transform: identity, year, month, day,
	// hour, bucket[N], truncate[N], void.
	// +kubebuilder:validation:Pattern=`^(identity|year|month|day|hour|void|bucket\[[0-9]+\]|truncate\[[0-9]+\])$`
	Transform string `json:"transform"`
}
