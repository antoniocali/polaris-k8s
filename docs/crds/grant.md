# PolarisGrant

Attaches a single Polaris privilege on a single target to a [`PolarisCatalogRole`](roles.md). One privilege per CR, deliberately — it keeps each grant individually addressable for revocation and auditing, rather than burying a privilege list inside a larger object where a diff has to be read carefully to see what actually changed.

## Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `catalogRoleRef` | [CatalogRoleRef](index.md#refs) | yes | The `PolarisCatalogRole` receiving the grant. |
| `privilege` | [Privilege](#privilege) | yes | The access right being granted. |
| `target` | [GrantTarget](#granttarget) | yes | The Polaris resource the privilege applies to. |

### Privilege

One of: `CATALOG_MANAGE_CONTENT`, `CATALOG_MANAGE_ACCESS`, `CATALOG_MANAGE_METADATA`, `CATALOG_READ_PROPERTIES`, `CATALOG_WRITE_PROPERTIES`, `NAMESPACE_CREATE`, `NAMESPACE_DROP`, `NAMESPACE_LIST`, `NAMESPACE_READ_PROPERTIES`, `NAMESPACE_WRITE_PROPERTIES`, `NAMESPACE_FULL_METADATA`, `TABLE_CREATE`, `TABLE_DROP`, `TABLE_LIST`, `TABLE_READ_PROPERTIES`, `TABLE_WRITE_PROPERTIES`, `TABLE_READ_DATA`, `TABLE_WRITE_DATA`, `TABLE_FULL_METADATA`, `VIEW_CREATE`, `VIEW_DROP`, `VIEW_LIST`, `VIEW_READ_PROPERTIES`, `VIEW_WRITE_PROPERTIES`, `VIEW_FULL_METADATA`.

### GrantTarget

Which sub-ref is required depends on `type` — enforced by CEL rules at admission, so `kubectl apply` rejects an inconsistent combination with a clear message rather than letting it reach Polaris.

| `type` | Required sub-refs |
|---|---|
| `catalog` | none — no sub-refs may be set |
| `namespace` | `namespaceRef` |
| `table` | `namespaceRef` and `tableRef` |
| `view` | `namespaceRef` and `viewRef` |

| Field | Type | Description |
|---|---|---|
| `type` | `catalog` \| `namespace` \| `table` \| `view` | Resource kind the privilege applies to. |
| `namespaceRef` | [NamespaceRef](index.md#refs) | Required for `namespace`, `table`, `view`. |
| `tableRef` | [TableRef](index.md#refs) | Required for `table`. |
| `viewRef` | [ViewRef](index.md#refs) | Required for `view`. |

## Status

| Field | Description |
|---|---|
| `conditions` | `Ready`, `Synced`. |

## Examples

Whole-catalog privilege:

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisGrant
metadata:
  name: lakehouse-manage
  namespace: data-platform
spec:
  catalogRoleRef:
    name: lakehouse-analytics-rw
  privilege: CATALOG_MANAGE_CONTENT
  target:
    type: catalog
```

Namespace-scoped write access:

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisGrant
metadata:
  name: analytics-rw-table-write
  namespace: data-platform
spec:
  catalogRoleRef:
    name: lakehouse-analytics-rw
  privilege: TABLE_WRITE_DATA
  target:
    type: namespace
    namespaceRef:
      name: analytics
```

Single-table read access:

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisGrant
metadata:
  name: orders-read
  namespace: data-platform
spec:
  catalogRoleRef:
    name: lakehouse-analytics-rw
  privilege: TABLE_READ_DATA
  target:
    type: table
    namespaceRef:
      name: analytics
    tableRef:
      name: orders
```
