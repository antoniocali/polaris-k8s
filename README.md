# polaris-k8s

**A Kubernetes operator that manages [Apache Polaris](https://polaris.apache.org/) (a REST Iceberg catalog) as Infrastructure-as-Code.**

Polaris exposes catalogs, namespaces, tables, principals, roles, and grants through a REST API. `polaris-k8s` makes those same resources declarative Kubernetes objects — so you can manage your Iceberg catalog the same way you manage everything else: with `kubectl apply`, GitOps, and pull requests instead of ad-hoc API calls.

> **Status: alpha.** All 12 reconcilers are implemented and unit-tested against a fake Polaris server. There's no envtest e2e suite yet, and no Helm chart is bundled (install via kustomize — see [DEPLOYMENT.md](DEPLOYMENT.md)). Schema/partition drift on tables and SQL drift on views aren't reconciled today (initial create is full-fidelity; further changes require drop+recreate).

## Why

Today the only way to configure Polaris is through its REST API: `curl` scripts, hand-rolled clients, or click-ops. That doesn't fit a GitOps workflow — no review, no audit trail, no drift detection, no rollback.

This operator turns Polaris configuration into Kubernetes CRDs, which means:

- **GitOps-native** — every catalog, namespace, role, and grant is a YAML file in a Git repo.
- **Reviewable changes** — schema changes, new principals, privilege grants all go through pull requests.
- **Declarative drift correction** — when reconcilers land, the operator continuously enforces desired state.
- **Standard tooling** — `kubectl`, ArgoCD, Helm, and any K8s-aware observability all just work.

> **Not the same as the [official Apache Polaris Helm chart](https://artifacthub.io/packages/helm/apache-polaris/polaris).** That chart deploys the Polaris **server**. This operator deploys a **controller that reconciles Polaris *resources*** — catalogs, namespaces, principals, roles, grants — as CRDs against an already-running Polaris server. They're complementary: run the server chart (or any Polaris), then point this operator at it via a `PolarisConnection`.

## Scope

`polaris-k8s` models the full Polaris management and catalog surface as 12 CRDs under `polaris.k8s.calific.io/v1alpha1`:

| Layer | CRD | Polaris resource |
|-------|-----|------------------|
| Connection | `PolarisConnection` | Server URL + OAuth client credentials |
| Catalog hierarchy | `PolarisCatalog` | Top-level catalog (S3 / Azure / GCS backed) |
| | `PolarisNamespace` | Logical container (nestable via `parentRef`) |
| | `PolarisTable` | Iceberg table (schema + partition spec) |
| | `PolarisView` | Iceberg view (SQL + dialect) |
| | `PolarisPolicy` | Namespace-scoped policy (compaction, retention, …) |
| Identity & access | `PolarisPrincipal` | User or service identity (generated creds → user-named Secret) |
| | `PolarisPrincipalRole` | Server-wide role |
| | `PolarisCatalogRole` | Catalog-scoped role |
| | `PolarisPrincipalRoleBinding` | Principal ↔ PrincipalRole |
| | `PolarisCatalogRoleBinding` | PrincipalRole ↔ CatalogRole |
| | `PolarisGrant` | Privilege grant on a target (catalog / namespace / table / view) |

```
PolarisConnection ──┬─► PolarisCatalog ──┬─► PolarisNamespace (self-nestable) ──┬─► PolarisTable
                    │                    │                                      ├─► PolarisView
                    │                    │                                      └─► PolarisPolicy
                    │                    └─► PolarisCatalogRole ◄── PolarisGrant
                    │
                    ├─► PolarisPrincipal (writes creds to a Secret you name)
                    └─► PolarisPrincipalRole

Bindings: Principal ─► PrincipalRole ─► CatalogRole
```

### Out of scope (today)

- Schema/partition drift on `PolarisTable` and SQL drift on `PolarisView` (initial create works; mutating those fields needs drop+recreate for now).
- Envtest e2e suite (unit tests only).
- Deployment tooling — no Helm chart or GitOps config is bundled; install via kustomize (see [DEPLOYMENT.md](DEPLOYMENT.md)).

## Quick example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisConnection
metadata:
  name: prod
  namespace: data-platform
spec:
  serverUrl: https://polaris.internal
  credentialsSecretRef:
    name: polaris-admin-credentials   # Secret with clientId / clientSecret
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalog
metadata:
  name: lakehouse
  namespace: data-platform
spec:
  connectionRef: { name: prod }
  defaultBaseLocation: s3://my-lakehouse/catalogs/lakehouse
  storageConfig:
    storageType: S3
    allowedLocations: [s3://my-lakehouse/catalogs/lakehouse]
    s3:
      roleArn: arn:aws:iam::123456789012:role/polaris
      region: eu-west-1
```

Full installation and end-to-end examples (namespaces, principals, grants) in [DEPLOYMENT.md](DEPLOYMENT.md).

**Try it on your laptop** — `hack/local-dev/up.sh` spins up kind + a real Apache Polaris + the operator + sample CRs with one command (Tilt-driven, no cloud storage needed). See [hack/local-dev/README.md](hack/local-dev/README.md).

## Layout

```
api/v1alpha1/      Go types + kubebuilder markers for the 12 CRDs
config/crd/bases/  Generated CRD YAML
internal/controller/  Reconcilers for all 12 CRDs (+ unit tests)
internal/polaris/  Polaris HTTP client — facade + generated sub-clients (management, catalog)
openapi/           Vendored Apache Polaris 1.4.1 OpenAPI specs — HTTP client is generated from these
cmd/main.go        Manager entrypoint
DEPLOYMENT.md      Install + sample CRs
CONTRIBUTING.md    How to contribute
CLAUDE.md          AI-agent-facing project conventions
```

## Versioning

| Component | Version |
|-----------|---------|
| CRD API | `polaris.k8s.calific.io/v1alpha1` |
| Apache Polaris (vendored spec) | 1.4.1 |
| Kubebuilder | v4.14 |
| Go | 1.26+ |

Until a `v1beta1` is published, the API may change without a conversion webhook — pin to a release tag in production.

## Contributing

Bug reports, feature requests, and PRs welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md) — it covers the dev setup, API conventions, and PR process.

## License

Apache License 2.0 — see source headers.
