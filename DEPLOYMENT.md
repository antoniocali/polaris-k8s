# Deployment

How to install the polaris-k8s operator on a Kubernetes cluster and declare your first Polaris resources.

> **Status: alpha.** All 12 reconcilers are implemented — applied CRs are reconciled against a live Polaris server (create/update/delete + status conditions + finalizers). Schema/partition drift on tables and SQL drift on views aren't reconciled (initial create is full-fidelity; later changes to those fields need drop+recreate).

> **Just want to try it on your laptop?** Skip this whole guide and use the
> local-dev harness — one command brings up kind + Apache Polaris + the operator
> + sample CRs: see [`hack/local-dev/README.md`](hack/local-dev/README.md).
> This document covers installing against a *real* cluster + Polaris.

> Two install paths: plain `kubectl`/kustomize (below, installs CRDs and the
> controller as two steps) or the Helm chart under [`dist/chart`](#installing-via-helm)
> (one step, more configurable). Both come from the same `config/` source —
> regenerate either with `make manifests` / `kubebuilder edit --plugins=helm/v2-alpha`.

## Prerequisites

- Kubernetes 1.27+ (CEL `x-kubernetes-validations` need 1.25+; we use map keys / list-type which are 1.27+ for full SSA correctness).
- `kubectl` configured against the target cluster.
- `make`, `go` 1.26+, and `docker` (or Colima) only if you intend to build the controller image yourself.
- A running Apache Polaris instance reachable from the cluster.

## 1. Install the CRDs

From a fresh checkout:

```sh
make manifests          # regenerate config/crd/bases/ from the Go types
kubectl apply -k config/crd
```

Verify:

```sh
kubectl api-resources --api-group=polaris.k8s.calific.io
# Expected: 12 resources (polarisconnections, polariscatalogs, polarisnamespaces,
# polaristables, polarisviews, polarispolicies, polarisprincipals,
# polarisprincipalroles, polariscatalogroles, polarisprincipalrolebindings,
# polariscatalogrolebindings, polarisgrants)
```

All resources are namespaced. They share the `polaris` category, so:

```sh
kubectl get polaris -A
```

returns every CR in the cluster.

## 2. Deploy the controller

The manager must reach both the Kubernetes API and your Polaris server. Build and push the image:

```sh
make docker-build docker-push IMG=<registry>/polaris-k8s:<tag>
```

Deploy the manager + RBAC into the cluster (default install namespace: `polaris-k8s-system`):

```sh
make deploy IMG=<registry>/polaris-k8s:<tag>
```

Tear down:

```sh
make undeploy
```

## Installing via Helm

Installs CRDs and the controller together, in one command, from the chart under [`dist/chart`](https://github.com/antoniocali/polaris-k8s/tree/main/dist/chart):

```sh
helm upgrade --install polaris-k8s ./dist/chart \
  --namespace polaris-k8s-system --create-namespace \
  --set manager.image.repository=<registry>/polaris-k8s \
  --set manager.image.tag=<tag> \
  --wait
```

or via the equivalent Makefile target (reads the image from `IMG`):

```sh
make helm-deploy IMG=<registry>/polaris-k8s:<tag>
```

Useful `values.yaml` knobs: `rbac.namespaced` (cluster-wide `ClusterRole` by default; set `true` for a single-namespace `Role`), `crd.keep` (keep CRDs — and every CR — on `helm uninstall`; defaults to `true`), `metrics.enable`/`prometheus.enable` for observability wiring, and `manager.resources` for the usual requests/limits. Full field-by-field reference: [calific.io/polaris-k8s/helm-chart/](https://calific.io/polaris-k8s/helm-chart/).

```sh
make helm-uninstall   # or: helm uninstall polaris-k8s -n polaris-k8s-system
```

## 3. Configure a Polaris connection

The connection holds the OAuth client credentials the operator uses to authenticate. Create a Secret with `clientId` and `clientSecret` keys, then a `PolarisConnection` pointing at it.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: polaris-admin-credentials
  namespace: polaris-tenant-a
type: Opaque
stringData:
  clientId: "<oauth-client-id>"
  clientSecret: "<oauth-client-secret>"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisConnection
metadata:
  name: prod
  namespace: polaris-tenant-a
spec:
  serverUrl: https://polaris.internal.example.com
  credentialsSecretRef:
    name: polaris-admin-credentials
    # clientIdKey / clientSecretKey default to "clientId" / "clientSecret"
```

For self-signed Polaris deployments:

```yaml
spec:
  caBundleSecretRef:
    name: polaris-ca
    key: ca.crt   # default
```

`insecureSkipVerify: true` is supported but discouraged outside of local dev.

## 4. Declare a catalog and namespace tree

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalog
metadata:
  name: lakehouse
  namespace: polaris-tenant-a
spec:
  connectionRef:
    name: prod
  type: INTERNAL
  defaultBaseLocation: s3://my-lakehouse/catalogs/lakehouse
  storageConfig:
    storageType: S3
    allowedLocations:
      - s3://my-lakehouse/catalogs/lakehouse
    s3:
      roleArn: arn:aws:iam::123456789012:role/polaris-lakehouse
      region: eu-west-1
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisNamespace
metadata:
  name: analytics
  namespace: polaris-tenant-a
spec:
  catalogRef:
    name: lakehouse
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisNamespace
metadata:
  name: analytics-sales
  namespace: polaris-tenant-a
spec:
  catalogRef:
    name: lakehouse
  parentRef:
    name: analytics
  name: sales              # final path segment in Polaris; here we override metadata.name
```

The nested namespace ends up as `analytics.sales` in Polaris.

## 5. Principals, roles, grants

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipal
metadata:
  name: airflow-worker
  namespace: polaris-tenant-a
spec:
  connectionRef:
    name: prod
  credentialsSecretRef:
    name: airflow-worker-polaris-creds   # operator creates/owns this Secret
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRole
metadata:
  name: analytics-writer
  namespace: polaris-tenant-a
spec:
  connectionRef:
    name: prod
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRole
metadata:
  name: lakehouse-analytics-rw
  namespace: polaris-tenant-a
spec:
  catalogRef:
    name: lakehouse
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisGrant
metadata:
  name: analytics-rw-table-write
  namespace: polaris-tenant-a
spec:
  catalogRoleRef:
    name: lakehouse-analytics-rw
  privilege: TABLE_WRITE_DATA
  target:
    type: namespace
    namespaceRef:
      name: analytics
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRoleBinding
metadata:
  name: analytics-writer-to-lakehouse-rw
  namespace: polaris-tenant-a
spec:
  principalRoleRef:
    name: analytics-writer
  catalogRoleRef:
    name: lakehouse-analytics-rw
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRoleBinding
metadata:
  name: airflow-worker-as-analytics-writer
  namespace: polaris-tenant-a
spec:
  principalRef:
    name: airflow-worker
  principalRoleRef:
    name: analytics-writer
```

The operator creates the principal in Polaris and writes the generated `clientId` / `clientSecret` into `airflow-worker-polaris-creds` (owned by the CR, so it's GC'd on delete). Consumers (Airflow DAGs, Spark jobs) mount that Secret and authenticate to Polaris.

## 6. Observe state

```sh
kubectl get polariscatalogs.polaris.k8s.calific.io -A
kubectl get polaris -A                       # everything in the category
kubectl describe polariscatalog lakehouse -n polaris-tenant-a
```

Every resource exposes a `Ready` condition (plus `Synced`, and per-kind extras like `AuthValid` on `PolarisConnection` and `CredentialsWritten` on `PolarisPrincipal`). The reconciler surfaces Polaris API errors via `status.conditions` and bumps `status.observedGeneration` on each successful pass:

```sh
kubectl get polarisconnection prod -n polaris-tenant-a \
  -o jsonpath='{.status.conditions}' | jq          # AuthValid + Ready
```

## Uninstall

```sh
kubectl delete -k config/crd     # removes CRDs and cascades CR deletion
make undeploy                    # removes controller deployment + RBAC
```

CRD deletion cascades to all CRs of that kind. Each CR carries a `polaris.k8s.calific.io/finalizer`, so deleting a CR first deletes the corresponding Polaris-side object, then removes the Kubernetes record. (If the `PolarisConnection` or its credentials Secret is already gone when a dependent is deleted — e.g. during `kubectl delete namespace` — the operator treats the remote as unreachable and drops the finalizer rather than wedging the object in `Terminating`.)

> Deleting the CRDs themselves removes finalizer enforcement, so prefer `kubectl delete` of individual CRs (or whole namespaces) while the controller is running if you want the Polaris-side objects cleaned up too.

## Troubleshooting

- **`kubectl apply` rejected with CEL message** — a cross-field invariant failed (e.g. `s3 must be set when storageType is S3`, or `target.namespaceRef is required when target.type is namespace`). The message names the offending field.
- **`kubectl apply` rejected with enum / pattern error** — check the field against the marker constants in `api/v1alpha1/*_types.go` (e.g. `WriteFormat`, `Privilege`).
- **`spec.name` looks empty on a fresh CR** — that's expected. Defaulting to `metadata.name` happens at reconcile time, not via a schema default, so the field stays empty in the stored object while the Polaris-side resource is still named after `metadata.name`.
- **CR stuck `Ready=False` with an `AuthenticationError` / `PolarisError`** — `kubectl describe` it; the condition message carries the Polaris HTTP status and body. Check the `PolarisConnection` is `Ready` first (everything chains off it).
- **CRDs install but `kubectl explain` shows no schema** — controller-runtime cached the old schema; `kubectl delete crd <name>` and reapply.
