# Helm chart

The chart lives at [`dist/chart/`](https://github.com/antoniocali/polaris-k8s/tree/main/dist/chart) and installs the CRDs and the controller together, in one release. It's generated from `config/` via kubebuilder's `helm/v2-alpha` plugin — **never hand-edit anything under `dist/chart/`**; regenerate it with `kubebuilder edit --plugins=helm/v2-alpha` after changing `config/crd`, `config/rbac`, or `config/manager` (same rule as `zz_generated.deepcopy.go`).

## Install

```sh
helm upgrade --install polaris-k8s ./dist/chart \
  --namespace polaris-k8s-system --create-namespace \
  --set manager.image.repository=<registry>/polaris-k8s \
  --set manager.image.tag=<tag> \
  --wait
```

or the equivalent Makefile target, which reads the image from `IMG`:

```sh
make helm-deploy IMG=<registry>/polaris-k8s:<tag>
```

Other lifecycle commands:

```sh
make helm-status      # helm status
make helm-history      # helm history
make helm-rollback     # helm rollback
make helm-uninstall    # helm uninstall
```

## Values reference

### `manager`

The controller Deployment.

| Key | Default | Description |
|---|---|---|
| `manager.enabled` | `true` | Set `false` to skip installing the manager Deployment entirely (CRDs/RBAC only). |
| `manager.replicas` | `1` | Pod replica count. Leader election (`--leader-elect`, always passed via `manager.args`) makes >1 safe. |
| `manager.image.repository` | `controller` | Image repository. Always set this — the default is a placeholder. |
| `manager.image.tag` | *(unset)* | Image tag. Defaults to `Chart.appVersion` when unset. |
| `manager.image.pullPolicy` | `IfNotPresent` | |
| `manager.imagePullSecrets` | *(unset)* | List of `{name: ...}` for a private registry. |
| `manager.args` | `["--leader-elect"]` | Extra manager CLI args. |
| `manager.podSecurityContext` | `runAsNonRoot: true`, seccomp `RuntimeDefault` | Pod-level `securityContext`. |
| `manager.securityContext` | no privilege escalation, all capabilities dropped, read-only rootfs | Container-level `securityContext`. |
| `manager.resources` | limits `500m`/`128Mi`, requests `10m`/`64Mi` | Standard resource requests/limits. |
| `manager.affinity` / `nodeSelector` / `tolerations` | empty | Standard pod scheduling controls. |
| `manager.strategy` | *(unset → Kubernetes default)* | Deployment update strategy. |
| `manager.priorityClassName` | *(unset)* | |
| `manager.topologySpreadConstraints` | *(unset)* | |
| `manager.terminationGracePeriodSeconds` | `10` | |
| `manager.labels` / `annotations` | *(unset)* | Extra labels/annotations on the Deployment. |
| `manager.pod.labels` / `pod.annotations` | *(unset)* | Extra labels/annotations on the Pod template. |

### `rbac`

| Key | Default | Description |
|---|---|---|
| `rbac.namespaced` | `false` | `false` installs a cluster-wide `ClusterRole`/`ClusterRoleBinding` (the operator can watch CRs in every namespace — matches how the CRDs themselves are namespaced-but-cluster-visible). `true` installs a namespace-scoped `Role`/`RoleBinding` in the release namespace only. |
| `rbac.helpers.enable` | `false` | Install the convenience `admin`/`editor`/`viewer` `ClusterRole`s per CRD kind (for wiring up your own `RoleBinding`s to give a team read-only or edit access to specific kinds). |

### `serviceAccount`

| Key | Default | Description |
|---|---|---|
| `serviceAccount.enable` | `true` | Create a `ServiceAccount` for the manager. Set `false` to bring your own (via `serviceAccount.name`). |
| `serviceAccount.name` | *(unset → chart fullname)* | Existing `ServiceAccount` name, only used when `enable: false`. |
| `serviceAccount.annotations` / `labels` | *(unset)* | |

### `crd`

| Key | Default | Description |
|---|---|---|
| `crd.enable` | `true` | Install the 12 CRDs as part of this release. Set `false` if you're managing them separately (e.g. via `kubectl apply -k config/crd` in a cluster where multiple releases share one CRD set). |
| `crd.keep` | `true` | Keep the CRDs (and therefore every CR, cascading through finalizers) when you `helm uninstall`. Set `false` to let uninstall remove them — **this deletes every Polaris-side object every CR in the cluster manages**, via their finalizers. |

### `metrics`

| Key | Default | Description |
|---|---|---|
| `metrics.enable` | `true` | Expose the `/metrics` endpoint via a `Service`. |
| `metrics.port` | `8443` | |
| `metrics.secure` | `true` | `true` serves HTTPS with authn/authz (requires the manager's `ClusterRole` to have metrics-auth access, already included); `false` serves plain HTTP. |

### `certManager`

| Key | Default | Description |
|---|---|---|
| `certManager.enable` | `false` | Provision TLS certs via [cert-manager](https://cert-manager.io/) for webhook/metrics-endpoint certificates. This project has no webhooks today (validation is CEL-only — see [CRD reference](crds/index.md)), so this is only relevant if you're securing the metrics endpoint with cert-manager-issued certs rather than the built-in self-signed ones. |

### `prometheus`

| Key | Default | Description |
|---|---|---|
| `prometheus.enable` | `false` | Install a Prometheus Operator `ServiceMonitor` for the metrics endpoint. Requires the Prometheus Operator CRDs to already be installed in the cluster. |

## Example: single-namespace RBAC with Prometheus scraping

```sh
helm upgrade --install polaris-k8s ./dist/chart \
  --namespace polaris-k8s-system --create-namespace \
  --set manager.image.repository=<registry>/polaris-k8s \
  --set manager.image.tag=<tag> \
  --set rbac.namespaced=true \
  --set prometheus.enable=true \
  --wait
```

!!! note "`rbac.namespaced=true` only watches CRs in the release namespace"
    Every `PolarisConnection`, `PolarisCatalog`, etc. you want reconciled has to live in the same namespace as the release. For a cluster shared across multiple teams' namespaces, leave `rbac.namespaced` at its default (`false`).
