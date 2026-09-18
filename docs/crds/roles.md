# Roles: PolarisPrincipalRole and PolarisCatalogRole

Two kinds, same tiny shape — a name and a property bag. What differs is scope: a `PolarisPrincipalRole` is server-wide and gets assigned to principals; a `PolarisCatalogRole` is scoped to one catalog and is the attachment point for [grants](grant.md). A [`PolarisCatalogRoleBinding`](bindings.md) is what connects the two.

## PolarisPrincipalRole

A server-wide role in Polaris that may be granted to one or more principals via [`PolarisPrincipalRoleBinding`](bindings.md).

### Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `connectionRef` | [ConnectionRef](index.md#refs) | yes | | The `PolarisConnection` this role lives on. |
| `name` | string | no | `.metadata.name` | Role name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `properties` | map[string]string | no | | Open-ended property bag. |

### Status

`conditions`: `Ready`, `Synced`.

### Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRole
metadata:
  name: analytics-writer
  namespace: data-platform
spec:
  connectionRef:
    name: prod
```

## PolarisCatalogRole

A role scoped to a single Polaris catalog. Privileges attach via [`PolarisGrant`](grant.md); principals reach it via a [`PolarisCatalogRoleBinding`](bindings.md) from a `PolarisPrincipalRole`.

### Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `catalogRef` | [CatalogRef](index.md#refs) | yes | | The `PolarisCatalog` this role is scoped to. |
| `name` | string | no | `.metadata.name` | Role name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `properties` | map[string]string | no | | Open-ended property bag. |

### Status

`conditions`: `Ready`, `Synced`.

### Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRole
metadata:
  name: lakehouse-analytics-rw
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse
```
