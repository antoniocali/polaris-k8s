# polaris-k8s

[![CI](https://github.com/antoniocali/polaris-k8s/actions/workflows/ci.yml/badge.svg)](https://github.com/antoniocali/polaris-k8s/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/antoniocali/polaris-k8s.svg)](https://pkg.go.dev/github.com/antoniocali/polaris-k8s)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**A Kubernetes operator that manages [Apache Polaris](https://polaris.apache.org/) (a REST Iceberg catalog) as Infrastructure-as-Code.**

**📖 [Full documentation](https://calific.io/polaris-k8s/)**: the why, a tutorial, installation, and a field-by-field reference for all 12 CRDs.

Polaris exposes catalogs, namespaces, tables, principals, roles, and grants through a REST API. `polaris-k8s` makes those same resources declarative Kubernetes objects. You manage your Iceberg catalog the same way you manage everything else, with `kubectl apply`, GitOps, and pull requests instead of ad-hoc API calls.

> **Status: alpha.** All 12 reconcilers are implemented, unit-tested against a fake Polaris server, covered by an envtest suite against a real Kubernetes API server, and proven end to end against a real Apache Polaris instance. Install via kustomize or Helm; see [DEPLOYMENT.md](DEPLOYMENT.md). Schema/partition drift on tables and SQL drift on views aren't reconciled today (initial create is full-fidelity; further changes require drop+recreate).

## Why

Today the only way to configure Polaris is through its REST API: `curl` scripts, hand-rolled clients, or click-ops. That doesn't fit a GitOps workflow. No review, no audit trail, no drift detection, no rollback.

This operator turns Polaris configuration into Kubernetes CRDs, which means:

- **GitOps-native.** Every catalog, namespace, role, and grant is a YAML file in a Git repo.
- **Reviewable changes.** Schema changes, new principals, and privilege grants all go through pull requests.
- **Declarative drift correction.** The operator continuously reconciles desired state on every change.
- **Standard tooling.** `kubectl`, ArgoCD, Helm, and any Kubernetes-aware observability all just work.

> **Not the same as the [official Apache Polaris Helm chart](https://artifacthub.io/packages/helm/apache-polaris/polaris).** That chart deploys the Polaris server. This operator deploys a controller that reconciles Polaris resources, such as catalogs, namespaces, principals, roles, and grants, as CRDs against an already-running Polaris server. They're complementary. Run the server chart, or any Polaris, then point this operator at it with a `PolarisConnection`.

## Scope

`polaris-k8s` models the full Polaris management and catalog surface as 12 CRDs under `polaris.k8s.calific.io/v1alpha1`:

| Layer | CRD | Polaris resource |
|-------|-----|------------------|
| Connection | `PolarisConnection` | Server URL and OAuth client credentials |
| Catalog hierarchy | `PolarisCatalog` | Top-level catalog (S3, Azure, or GCS backed) |
| | `PolarisNamespace` | Logical container, nestable via `parentRef` |
| | `PolarisTable` | Iceberg table (schema and partition spec) |
| | `PolarisView` | Iceberg view (SQL and dialect) |
| | `PolarisPolicy` | Namespace-scoped policy, such as compaction or retention |
| Identity & access | `PolarisPrincipal` | User or service identity; generated credentials go to a named Secret |
| | `PolarisPrincipalRole` | Server-wide role |
| | `PolarisCatalogRole` | Catalog-scoped role |
| | `PolarisPrincipalRoleBinding` | Connects a Principal to a PrincipalRole |
| | `PolarisCatalogRoleBinding` | Connects a PrincipalRole to a CatalogRole |
| | `PolarisGrant` | Privilege grant on a target (catalog, namespace, table, or view) |

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

- Schema/partition drift on `PolarisTable` and SQL drift on `PolarisView`. Initial create works; mutating those fields needs drop+recreate for now.

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

Full installation and end-to-end examples (namespaces, principals, grants) are in [DEPLOYMENT.md](DEPLOYMENT.md).

**Try it on your laptop.** `hack/local-dev/up.sh` spins up kind, a real Apache Polaris, the operator, and sample CRs with one command (Tilt-driven, no cloud storage needed). See [hack/local-dev/README.md](hack/local-dev/README.md).

## Layout

```
api/v1alpha1/      Go types + kubebuilder markers for the 12 CRDs
config/crd/bases/  Generated CRD YAML
internal/controller/  Reconcilers for all 12 CRDs (+ unit tests)
internal/polaris/  Polaris HTTP client: facade plus generated sub-clients (management, catalog)
openapi/           Vendored Apache Polaris 1.4.1 OpenAPI specs (the HTTP client is generated from these)
cmd/main.go        Manager entrypoint
dist/chart/        Helm chart, generated from config/ (regenerate with `kubebuilder edit --plugins=helm/v2-alpha`)
docs/              GitHub Pages documentation site (Zensical, config in mkdocs.yml)
DEPLOYMENT.md      Install + sample CRs
CONTRIBUTING.md    How to contribute
RELEASING.md       How a merge to main becomes a tagged release and published artifacts
CLAUDE.md          AI-agent-facing project conventions
```

## Versioning

| Component | Version |
|-----------|---------|
| CRD API | `polaris.k8s.calific.io/v1alpha1` |
| Apache Polaris (vendored spec) | 1.4.1 |
| Kubebuilder | v4.14 |
| Go | 1.26+ |
| Helm chart (`dist/chart`) | 0.1.0 |

Until a `v1beta1` is published, the API may change without a conversion webhook. Pin to a release tag in production.

Releases are tagged `vX.Y.Z` and published automatically once merged; see [RELEASING.md](RELEASING.md) for the full process, including how the Helm chart version and the Apache Polaris compatibility note stay in sync with each tag. Images: `ghcr.io/antoniocali/polaris-k8s`. Helm chart: `oci://ghcr.io/antoniocali/charts/polaris-k8s`.

## Contributing

Bug reports, feature requests, and PRs welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md). It covers the dev setup, API conventions, and PR process.

## License

Apache License 2.0. See source headers.
