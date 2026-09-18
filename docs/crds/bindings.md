# Bindings: PolarisPrincipalRoleBinding and PolarisCatalogRoleBinding

Access flows through Polaris in two hops, and each hop is its own binding kind. That's deliberate. Either hop can be removed independently without touching the other side, and a many-to-many relationship is just multiple small CRs rather than one CR with a list that grows without a clear owner.

A `PolarisPrincipalRoleBinding` connects a principal to a principal role. A `PolarisCatalogRoleBinding` connects that principal role to a catalog role. A [`PolarisGrant`](grant.md) is what then attaches a privilege to the catalog role.

## PolarisPrincipalRoleBinding

Grants a [`PolarisPrincipalRole`](roles.md) to a [`PolarisPrincipal`](principal.md).

### Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `principalRef` | [PrincipalRef](index.md#refs) | yes | The `PolarisPrincipal` being granted the role. |
| `principalRoleRef` | [PrincipalRoleRef](index.md#refs) | yes | The `PolarisPrincipalRole` being assigned. |

### Status

`conditions`: `Ready`, `Synced`.

### Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRoleBinding
metadata:
  name: airflow-worker-as-analytics-writer
  namespace: data-platform
spec:
  principalRef:
    name: airflow-worker
  principalRoleRef:
    name: analytics-writer
```

## PolarisCatalogRoleBinding

Grants a [`PolarisCatalogRole`](roles.md) to a [`PolarisPrincipalRole`](roles.md). This is the bridge that lets everyone holding that principal role inherit the catalog role's grants.

### Spec

| Field | Type | Required | Description |
|---|---|---|---|
| `principalRoleRef` | [PrincipalRoleRef](index.md#refs) | yes | The `PolarisPrincipalRole` being granted access. |
| `catalogRoleRef` | [CatalogRoleRef](index.md#refs) | yes | The `PolarisCatalogRole` being attached. |

### Status

`conditions`: `Ready`, `Synced`.

### Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRoleBinding
metadata:
  name: analytics-writer-to-lakehouse-rw
  namespace: data-platform
spec:
  principalRoleRef:
    name: analytics-writer
  catalogRoleRef:
    name: lakehouse-analytics-rw
```
