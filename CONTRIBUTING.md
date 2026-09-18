# Contributing to polaris-k8s

Thanks for considering a contribution. This document covers the dev setup, conventions, and PR process.

## Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.26+ |
| `make` | any recent |
| `kubebuilder` | v4.14+ |
| `kubectl` | v1.27+ |
| `docker` (or Colima) | only needed if you intend to build the manager image |
| `helm` | only needed if you're working on `dist/chart/` |

A local Kubernetes cluster (kind) is only needed for `make test-e2e` or the local-dev harness. The envtest suite (`make test`) downloads its own standalone kube-apiserver/etcd binaries and needs no cluster at all — same as pure API/CRD work.

## Setup

```sh
git clone git@github.com:antoniocali/polaris-k8s.git
cd polaris-k8s
go mod download
make manifests generate                # regenerate CRD YAML + deepcopy code
go test ./api/... -count=1             # fast sanity check
make test                              # full test suite (downloads envtest binaries on first run)
```

## Local development (Tilt + kind + Polaris)

For anything beyond pure API/CRD work — writing or changing reconcilers, or just
watching the operator drive a real server — use the local-dev harness. One
command stands up a [kind](https://kind.sigs.k8s.io/) cluster, a real Apache
Polaris (in Docker, FILE storage, no S3/MinIO needed), the operator (run on your
host so edits rebuild instantly), and a set of sample CRs:

```sh
brew install tilt   # if `tilt version` fails; kind/kubectl/docker assumed present

make local-up                    # kind + Polaris + operator + sample CRs; Tilt UI at :10350
make local-down                  # tear it all down
# (these wrap hack/local-dev/up.sh and down.sh, which you can also run directly)
```

Three ways to drive and inspect it:

- **Tilt dashboard** (http://localhost:10350) — operator + Polaris logs and every CR's status in one place.
- **kubectl** — `source hack/local-dev/env.sh`, then `kubectl -n polaris-dev get polaris`, edit/apply/delete CRs and watch them re-reconcile.
- **Polaris REST API** — `hack/local-dev/polaris-api.sh GET /api/management/v1/catalogs` (Polaris has no web UI; this is the token-authenticated way to read server truth).

`up.sh` isolates `KUBECONFIG` to the kind cluster and the Tiltfile refuses any
other context, so the harness can never touch a real cluster. Full details and
the FILE-storage caveats live in [`hack/local-dev/README.md`](hack/local-dev/README.md).

## What to work on

- **Found a bug?** Open a GitHub Issue first so we can triage. Small typo/doc fixes can go straight to PR.
- **Want a new CRD field or kind?** Open an Issue describing the Polaris-side capability you're trying to model. Schema changes are easy to ship while we're on `v1alpha1` but get expensive once we've promoted to `v1beta1`, so we want to be deliberate.

## Branching, commits, PRs

- Branch from `main` using a descriptive name: `feat/polaris-connection-reconciler`, `fix/grant-cel-rule`, `docs/deployment-azure`.
- One logical change per PR. If you find yourself writing "and also …" in the description, split it.
- Commit messages: short imperative subject (≤72 chars), blank line, body explaining the *why*. We don't enforce Conventional Commits but the spirit applies.
- Rebase on `main` before opening the PR; squash merges are the default.
- PR description should answer: **what changed**, **why**, and **how it was tested**.

## Testing requirements

Every PR must keep the existing test suite green.

```sh
make fmt vet         # formatting + go vet
go test ./api/... -count=1   # API-level tests (fast, no cluster)
make test            # full suite incl. envtest
```

When adding new code:

- **API changes** (`api/v1alpha1/*_types.go`) — extend `api_validation_test.go` so the new field/type is covered by round-trip + deepcopy tests. Inspect the generated CRD YAML after `make manifests` and confirm required-ness, defaults, enums, and list semantics rendered as intended.
- **Controller logic** — cover the happy path (and any new branch) in the matching `internal/controller/*_unit_test.go`, using a `httptest.Server` to fake Polaris rather than requiring a live server. If the change touches something only a real API server would catch (CRD schema, status subresource, finalizer/deletion timing), add or extend the matching `*_controller_test.go` envtest spec too — see `polarisconnection_controller_test.go` for the established pattern (real parent chain seeded via `Create` + `Status().Update()`, fake Polaris backend via `BuildPolarisClient`).
- **Bug fixes** — add a regression test in the same PR. "Reproduces the bug" → "fix" → "test passes" should be visible in the diff.

## API design conventions

These are project-specific rules that the kubebuilder defaults don't enforce. They mirror the AI-agent-facing notes in [CLAUDE.md](CLAUDE.md):

- **One API group, one version (`polaris.k8s.calific.io/v1alpha1`)** until we ship `v1beta1`. All kinds are Namespaced.
- **Purpose-built `*Ref` structs** in `api/v1alpha1/refs.go` — never `corev1.LocalObjectReference` / `corev1.ObjectReference`. The structural test in `api_validation_test.go` enforces this; don't suppress it.
- **Hierarchy via parent ref**, not path arrays (`PolarisNamespace.parentRef`).
- **Polaris-side name defaults to `metadata.name`** when `spec.name` is unset. Naming pattern: `^[a-zA-Z0-9_-]+$`.
- **Every CRD has** `+kubebuilder:subresource:status`, a `[]metav1.Condition` field with `listType=map`/`listMapKey=type`, `observedGeneration`, printer columns for `Ready` and `Age`, and `categories=polaris`.
- **Cross-field rules → CEL** (`+kubebuilder:validation:XValidation`). Examples in `PolarisCatalog.StorageConfig` and `PolarisGrant.Spec`.
- **List semantics matter for SSA.** Use `+listType=set` for unique-item lists, `+listType=map`+`+listMapKey=...` for keyed structs. Required for clean ArgoCD diffs.
- **Never edit `zz_generated.deepcopy.go`** — `make generate` regenerates it.

## Updating the vendored Polaris OpenAPI specs

The HTTP client (`internal/polaris/`) is generated around `openapi/*.yaml`, which pins to a specific Apache Polaris release. To bump:

```sh
# Replace VERSION with the target Polaris release tag (e.g. apache-polaris-1.5.0)
VERSION=apache-polaris-1.4.1
curl -fsSL -o openapi/polaris-management-service.yaml \
  https://raw.githubusercontent.com/apache/polaris/refs/tags/$VERSION/spec/polaris-management-service.yml
curl -fsSL -o openapi/polaris-catalog-service.yaml \
  https://raw.githubusercontent.com/apache/polaris/refs/tags/$VERSION/spec/generated/bundled-polaris-catalog-service.yaml
```

Also update:

- `openapi/README.md` — the recorded version + date.
- `README.md` — the "Apache Polaris (vendored spec)" row in the versioning table.

If a bump changes the schema in ways the operator relies on, regenerate the client:

```sh
make polaris-client-gen   # downloads oapi-codegen if missing, runs hack/prepare-specs.sh, regenerates internal/polaris/{management,catalog}/zz_generated.gen.go
make test                 # confirm nothing breaks
```

The `hack/prepare-specs.sh` script applies local renames to the vendored specs before generation (e.g. the catalog spec has a parameter `namespace` that collides with the `Namespace` schema in Go). Patched copies live under `bin/oapi/` and are gitignored.

## Regenerating CRDs after type changes

```sh
make generate     # regenerate api/v1alpha1/zz_generated.deepcopy.go
make manifests    # regenerate config/crd/bases/*.yaml
```

Both are wired into `make test` and `make build`, so CI will fail if you forget. Commit the regenerated files alongside the type change.

If the change affects RBAC, the manager, or a CRD (anything under `config/crd`, `config/rbac`, or `config/manager`), also regenerate the Helm chart and installer bundle so they don't drift from `config/`:

```sh
kubebuilder edit --plugins=helm/v2-alpha   # regenerates dist/chart/ and dist/install.yaml from config/
```

Never hand-edit anything under `dist/chart/` or `dist/install.yaml` — same rule as `zz_generated.deepcopy.go`.

## Code style

- `gofmt` / `goimports` — `make fmt` covers this.
- Run `make lint` (`golangci-lint`) before opening a PR.
- Comments on exported types and fields. Field doc-comments become `kubectl explain` output for end users — write for that audience.

## Reporting security issues

See [SECURITY.md](SECURITY.md) — don't open a public GitHub Issue for security problems.

## License

By contributing, you agree your contribution is licensed under the Apache License 2.0, matching the rest of the project.
