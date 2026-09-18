# CRD reference: overview

All 12 kinds live in the API group `polaris.k8s.calific.io/v1alpha1` and are namespaced. They share the `polaris` kubectl category, so `kubectl get polaris -A` lists all of them. Every kind has a `Ready` condition, a `Synced` condition, and `.status.observedGeneration`. Every kind except `PolarisConnection` also has a `polaris.k8s.calific.io/finalizer` that deletes the Polaris-side object before the Kubernetes record disappears. `PolarisConnection` skips this because it owns no remote state of its own.

## Naming

A resource's name in Polaris defaults to `.metadata.name`. Every kind accepts an optional `spec.name` to override that. This matters when the Polaris-side name needs characters a Kubernetes object name doesn't allow. Kubernetes names are RFC 1123 subdomains: lowercase alphanumerics and `-` only. Polaris names additionally allow `_`.

## Hierarchy

```
PolarisConnection ──┬─► PolarisCatalog ──┬─► PolarisNamespace (self-nestable via parentRef) ──┬─► PolarisTable
                    │                    │                                                    ├─► PolarisView
                    │                    │                                                    └─► PolarisPolicy
                    │                    └─► PolarisCatalogRole ◄── PolarisGrant (privilege on catalog|namespace|table|view)
                    │
                    ├─► PolarisPrincipal (writes generated creds to spec.credentialsSecretRef)
                    └─► PolarisPrincipalRole

PolarisPrincipalRoleBinding: Principal ── grants ──► PrincipalRole
PolarisCatalogRoleBinding:   PrincipalRole ── inherits ──► CatalogRole
```

## Ref types {#refs}

Every `*Ref` field, such as `connectionRef`, `catalogRef`, `namespaceRef`, `principalRef`, `principalRoleRef`, `catalogRoleRef`, `tableRef`, or `viewRef`, has the same shape:

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Name of the referenced object. |
| `namespace` | string | no | Defaults to the referrer's own namespace, resolved at reconcile time. |

This project deliberately avoids `corev1.LocalObjectReference` and `corev1.ObjectReference`. Purpose-built `*Ref` structs keep each reference's cross-namespace behavior explicit and independently documented.

## Kinds at a glance

| Kind | Short name | Parent(s) | Purpose |
|---|---|---|---|
| [`PolarisConnection`](connection.md) | `pconn` | none | Server URL and OAuth credentials. Everything else attaches here, directly or through a parent. |
| [`PolarisCatalog`](catalog.md) | `pcat` | Connection | A top-level Iceberg catalog and its storage config. |
| [`PolarisNamespace`](namespace.md) | `pns` | Catalog, plus an optional parent Namespace | A logical container for tables and views, nestable. |
| [`PolarisTable`](table.md) | `ptbl` | Namespace | An Iceberg table: schema, partition spec, write format. |
| [`PolarisView`](view.md) | `pview` | Namespace | An Iceberg view: schema plus a SQL definition. |
| [`PolarisPolicy`](policy.md) | `ppol` | Namespace | A typed policy, such as compaction, attached to a namespace. |
| [`PolarisPrincipal`](principal.md) | `pprin` | Connection | An identity. The operator writes its generated credentials to a Secret. |
| [`PolarisPrincipalRole`](roles.md) | `pprole` | Connection | A server-wide role, assignable to principals. |
| [`PolarisCatalogRole`](roles.md) | `pcrole` | Catalog | A catalog-scoped role, the attachment point for grants. |
| [`PolarisPrincipalRoleBinding`](bindings.md) | `pprb` | Principal, PrincipalRole | Assigns a principal role to a principal. |
| [`PolarisCatalogRoleBinding`](bindings.md) | `pcrb` | PrincipalRole, CatalogRole | Lets a principal role's holders inherit a catalog role's grants. |
| [`PolarisGrant`](grant.md) | `pgrant` | CatalogRole, plus a target | Attaches one privilege on one target to a catalog role. |

Not sure where to start? The [Tutorial](../tutorial.md) builds all twelve in dependency order, with a working example of each.
