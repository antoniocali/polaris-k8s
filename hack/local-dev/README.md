# Local-dev harness

Run the whole operator loop on your laptop: a real Apache Polaris, the CRDs in a
[kind](https://kind.sigs.k8s.io/) cluster, the operator, and a set of sample CRs
that reconcile against Polaris — all driven by [Tilt](https://tilt.dev).

```
┌─ your host ─────────────────────────────────────────────────────────┐
│  Tilt                                                                │
│   ├─ docker compose ──► Apache Polaris   (localhost:8181 / :8182)     │
│   ├─ make install ────► CRDs ─┐                                       │
│   ├─ go run ./cmd ────► operator ──┐  (host process)                  │
│   └─ kubectl apply ──► sample CRs  │                                  │
│                         │          │                                  │
│                    kind cluster ◄──┘  watches CRs, calls Polaris ─────┘
└──────────────────────────────────────────────────────────────────────┘
```

The operator runs **on the host** (not in-cluster) so it can reach Polaris at
`http://localhost:8181` directly and watch kind via kubeconfig — which keeps the
edit→rebuild loop instant and avoids in-cluster networking.

## Prerequisites

`kind`, `kubectl`, `tilt`, `docker` (Colima is fine), and `go`. All common
on a typical macOS dev setup except possibly Tilt:

```sh
brew install tilt   # if `tilt version` fails
```

## Up / down

```sh
hack/local-dev/up.sh     # creates the kind cluster, then `tilt up` (UI at :10350)
hack/local-dev/down.sh   # tilt down + compose down + delete the kind cluster
```

Or via the Makefile from the repo root: `make local-up` / `make local-down`.

`up.sh` writes an **isolated** `KUBECONFIG` (`hack/local-dev/.kubeconfig`,
git-ignored) containing only the kind cluster, and the Tiltfile hard-fails on any
context other than `kind-polaris-dev`. Your `prod`/`staging` contexts are never
reachable from this harness.

## What you get

Namespace `polaris-dev`, a `PolarisConnection` to the local server, and a full
slice of the object graph — a **FILE-backed** `PolarisCatalog`, a namespace, a
principal (with its generated credentials Secret), roles, a grant, and both
bindings. All reconcile to `Ready=True`.

## Three ways to drive & inspect it

### 1. Tilt dashboard — http://localhost:10350

The harness UI. One place for Polaris + operator logs and every CR's status,
with buttons to re-trigger a resource. Opened automatically by `up.sh`.

### 2. kubectl (the kind cluster)

`up.sh` registers the `kind-polaris-dev` context in your default `~/.kube/config`,
so from any shell you can target it explicitly:

```sh
kubectl --context kind-polaris-dev -n polaris-dev get polaris
```

Or point the shell at the harness's kubeconfig (also sets `POLARIS_*` vars) and
drop the `--context` flag:

```sh
source hack/local-dev/env.sh          # KUBECONFIG → kind-polaris-dev (+ POLARIS_* vars)

kubectl -n polaris-dev get polaris                    # every CR + Ready column
kubectl -n polaris-dev describe polariscatalog lakehouse
kubectl -n polaris-dev get polariscatalog lakehouse -o jsonpath='{.status.conditions}' | jq

# Edit a CR and watch it re-reconcile (Tilt shows the operator log):
kubectl -n polaris-dev edit polariscatalog lakehouse
kubectl -n polaris-dev apply -f my-extra-cr.yaml
kubectl -n polaris-dev delete polaristable orders        # finalizer deletes it in Polaris too

# The principal's generated credentials Secret:
kubectl -n polaris-dev get secret airflow-polaris-creds -o jsonpath='{.data.clientId}' | base64 -d
```

### 3. Polaris REST API (there is no Polaris web UI)

Apache Polaris ships **no web console** — its surface is the REST API. The
helper handles the OAuth token for you:

```sh
hack/local-dev/polaris-api.sh GET /api/management/v1/catalogs
hack/local-dev/polaris-api.sh GET /api/management/v1/catalogs/lakehouse
hack/local-dev/polaris-api.sh GET /api/management/v1/principals
hack/local-dev/polaris-api.sh GET /api/catalog/v1/lakehouse/namespaces      # data plane
hack/local-dev/polaris-api.sh GET /api/catalog/v1/lakehouse/namespaces/analytics/tables
```

Cross-check that what the operator did via CRs actually landed in Polaris:
`kubectl get polariscatalog` (desired/status) ↔ `polaris-api.sh GET .../catalogs` (server truth).

Health & metrics are at http://localhost:8182/q/health and `/q/metrics`.

## FILE storage (testing only)

Polaris ships with `FILE` storage **disabled** — its production-readiness gate
treats it as a severe risk and aborts startup. `docker-compose.yaml` enables it
purely for local testing via three properties (see the comments there):

- `polaris.features."SUPPORTED_CATALOG_STORAGE_TYPES"` — adds `FILE`
- `polaris.features."ALLOW_INSECURE_STORAGE_TYPES"` — permits the FILE `HadoopFileIO`
- `polaris.readiness.ignore-severe-issues` — don't abort startup on the FILE warning

This is what lets the catalog/namespace/table data plane work with no S3/MinIO.
**Never** set these on a real Polaris. To exercise S3 instead, point a catalog's
`storageConfig` at MinIO/RustFS and drop the FILE flags.

## Notes

- The operator's metrics + health servers are disabled (`=0`) by the Tiltfile's
  `go run` — not needed locally, and it avoids a fixed port colliding with a
  stale/orphaned operator process on restart.
- Polaris uses an in-memory metastore: everything resets on `docker compose down`.
- Image is pinned to `apache/polaris:latest`; pin to a release digest in
  `docker-compose.yaml` if you need reproducibility.
