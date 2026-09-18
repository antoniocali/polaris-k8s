# PolarisTable

An Iceberg table managed inside a Polaris namespace.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `namespaceRef` | [NamespaceRef](index.md#refs) | yes | | The `PolarisNamespace` this table lives in. |
| `name` | string | no | `.metadata.name` | Table name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `schema` | [IcebergSchema](#icebergschema) | yes | | The table's Iceberg schema. |
| `partitionSpec` | [IcebergPartitionSpec](#icebergpartitionspec) | no | | Partitioning strategy. |
| `writeFormat` | `parquet` \| `orc` \| `avro` | no | `parquet` | Default data file format for new writes. |
| `properties` | map[string]string | no | | Open-ended property bag (e.g. `write.target-file-size-bytes`). |

### IcebergSchema

| Field | Type | Required | Description |
|---|---|---|---|
| `fields` | [][IcebergField](#icebergfield) | yes (≥1) | Columns, in declaration order. |
| `identifierFieldIds` | []int32 | no | Primary-key field IDs, if any. |

### IcebergField

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `id` | int32 | yes | | Iceberg field ID, stable across schema evolution. |
| `name` | string | yes | | Column name. |
| `type` | string | yes | | Iceberg type: `int`, `long`, `string`, `decimal(10,2)`, a nested `struct<...>`, and so on. Validation is intentionally loose. Polaris is the source of truth for valid types. |
| `required` | bool | no | `false` | Whether the field is `NOT NULL`. |
| `doc` | string | no | | Human-readable column description. |

### IcebergPartitionSpec

| Field | Type | Required | Description |
|---|---|---|---|
| `fields` | [][IcebergPartitionField](#icebergpartitionfield) | no | Partition transforms applied to source columns. |

### IcebergPartitionField

| Field | Type | Required | Description |
|---|---|---|---|
| `sourceId` | int32 | yes | ID of the source schema field this partition derives from. |
| `fieldId` | int32 | yes | ID of the partition field itself. |
| `name` | string | yes | Partition column name. |
| `transform` | string | yes | One of `identity`, `year`, `month`, `day`, `hour`, `void`, `bucket[N]`, `truncate[N]`. |

## Status

| Field | Description |
|---|---|
| `location` | Resolved on-disk URI of the table. |
| `tableUuid` | Stable Iceberg UUID of the table. |
| `currentSnapshotId` | Most recently committed snapshot, if any. |
| `conditions` | `Ready`, `Synced`. |

## Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisTable
metadata:
  name: orders
  namespace: data-platform
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
        required: true
      - id: 2
        name: amount
        type: "decimal(10,2)"
  writeFormat: parquet
```

!!! warning "Schema/partition drift isn't reconciled"
    Initial create is full-fidelity, but changes to `spec.schema` or `spec.partitionSpec` on an existing table aren't detected or applied. That requires the Iceberg `CommitTable` machinery, which isn't implemented yet. Changing a table's schema today means dropping and recreating the CR. See [Why](../index.md) for why this project leans away from managing ongoing schema evolution this way in the first place.
