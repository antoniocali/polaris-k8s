# PolarisPolicy

Attaches a typed policy, such as compaction or retention, to a Polaris namespace.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `namespaceRef` | [NamespaceRef](index.md#refs) | yes | | The `PolarisNamespace` this policy applies to. |
| `name` | string | no | `.metadata.name` | Policy name in Polaris. Pattern `^[a-zA-Z0-9_.-]+$`. |
| `type` | string | yes | | Polaris policy type, e.g. `system.data-compaction`. |
| `description` | string | no | | Human-readable description. |
| `content` | JSON object | yes | | The policy body. Its schema is defined by `type`. Polaris validates it at apply time, not this CRD. |

## Status

| Field | Description |
|---|---|
| `conditions` | `Ready`, `Synced`. |

## Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPolicy
metadata:
  name: compact
  namespace: data-platform
spec:
  namespaceRef:
    name: analytics
  type: system.data-compaction
  content:
    target_file_size_mb: 256
```

!!! note "Content schema is Polaris's, not ours"
    `content` is validated by Polaris against a schema specific to `type`, one this CRD has no visibility into. Check Polaris's own documentation for the policy type you're using to get the shape right. An incorrect `content` payload surfaces as a `PolarisError` status condition, not as a rejection at apply time.
