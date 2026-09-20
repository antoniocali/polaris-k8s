# PolarisView

An Iceberg view managed inside a Polaris namespace: an output schema plus a SQL definition.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `namespaceRef` | [NamespaceRef](index.md#refs) | yes | | The `PolarisNamespace` this view lives in. |
| `name` | string | no | `.metadata.name` | View name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `schema` | [IcebergSchema](table.md#icebergschema) | yes | | Output schema of the view. |
| `sql` | string | yes | | The view definition. |
| `dialect` | string | no | `spark` | SQL dialect `sql` is written in. |
| `properties` | map[string]string | no | | Open-ended property bag. |

## Status

| Field | Description |
|---|---|
| `versionId` | Integer version-id of the current view representation. |
| `conditions` | `Ready`, `Synced`. |

## Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisView
metadata:
  name: orders-summary
  namespace: data-platform
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
  sql: "SELECT id FROM analytics.orders"
```

!!! warning "SQL drift isn't reconciled"
    Same caveat as `PolarisTable`: initial create is full-fidelity, but changes to `spec.sql` or `spec.schema` on an existing view aren't picked up. This project deliberately doesn't build the `CommitView` machinery that would take. Recreate the CR to change a view's definition. See [Why](../index.md) for the reasoning.
