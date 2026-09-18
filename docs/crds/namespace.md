# PolarisNamespace

A logical container for tables and views inside a catalog. Nesting is expressed by chaining `parentRef` rather than carrying a full path on a single CR, so each level has its own lifecycle, `ownerRef` chain, and reconciliation status.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `catalogRef` | [CatalogRef](index.md#refs) | yes | | The `PolarisCatalog` this namespace lives under. |
| `parentRef` | [NamespaceRef](index.md#refs) | no | | Makes this a nested namespace under another `PolarisNamespace`. Both refs must resolve to the same catalog. |
| `name` | string | no | `.metadata.name` | Final path segment in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `properties` | map[string]string | no | | Open-ended property bag. Common keys: `location`, `owner`. |

## Status

| Field | Description |
|---|---|
| `fullPath` | Fully qualified namespace path, resolved by walking `parentRef`. For example `["analytics", "sales"]`. |
| `conditions` | `Ready`, `Synced`. |

## Nesting example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisNamespace
metadata:
  name: analytics
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisNamespace
metadata:
  name: analytics-sales
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse
  parentRef:
    name: analytics
  name: sales              # final Polaris-side path segment; overrides metadata.name
```

The nested namespace ends up as `analytics.sales` in Polaris. That's why `metadata.name` had to be `analytics-sales`, since Kubernetes names must be unique within a namespace across the whole nesting tree, while `spec.name` carries the actual path segment.

!!! note
    Enforcing that `parentRef` resolves to the same `catalogRef` as this namespace is a reconciler check, not something the CRD schema alone can express. If you see it rejected, it's here, not in Polaris.
