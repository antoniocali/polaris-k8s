# Helm chart

The chart lives at [`dist/chart/`](https://github.com/antoniocali/polaris-k8s/tree/main/dist/chart) and installs the CRDs and the controller together, in one release. It's generated from `config/` via kubebuilder's `helm/v2-alpha` plugin. Never hand-edit anything under `dist/chart/`. Regenerate it with `kubebuilder edit --plugins=helm/v2-alpha` after changing `config/crd`, `config/rbac`, or `config/manager`, the same way you'd regenerate `zz_generated.deepcopy.go`.

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
| `manager.replicas` | `1` | Pod replica count. Leader election is always on (`--leader-elect`, via `manager.args`), so more than one replica is safe. |
| `manager.image.repository` | `controller` | Image repository. Always set this; the default is a placeholder. |
| `manager.image.tag` | *(unset)* | Image tag. Falls back to `Chart.appVersion` when unset. |
| `manager.image.pullPolicy` | `IfNotPresent` | |
| `manager.imagePullSecrets` | *(unset)* | List of `{name: ...}` for a private registry. |
| `manager.args` | `["--leader-elect"]` | Extra manager CLI args. |
| `manager.podSecurityContext` | non-root, seccomp `RuntimeDefault` | Pod-level `securityContext`. |
| `manager.securityContext` | no privilege escalation, capabilities dropped, read-only rootfs | Container-level `securityContext`. |
| `manager.resources` | limits `500m`/`128Mi`, requests `10m`/`64Mi` | Standard resource requests and limits. |
| `manager.affinity`, `nodeSelector`, `tolerations` | empty | Standard pod scheduling controls. |
| `manager.strategy` | *(unset, falls back to the Kubernetes default)* | Deployment update strategy. |
| `manager.priorityClassName` | *(unset)* | |
| `manager.topologySpreadConstraints` | *(unset)* | |
| `manager.terminationGracePeriodSeconds` | `10` | |
| `manager.labels`, `annotations` | *(unset)* | Extra labels and annotations on the Deployment. |
| `manager.pod.labels`, `pod.annotations` | *(unset)* | Extra labels and annotations on the Pod template. |

### `rbac`

| Key | Default | Description |
|---|---|---|
| `rbac.namespaced` | `false` | When `false`, installs a cluster-wide `ClusterRole` and `ClusterRoleBinding`, so the operator can watch CRs in every namespace. When `true`, installs a namespace-scoped `Role` and `RoleBinding` in the release namespace only. |
| `rbac.helpers.enable` | `false` | Install the convenience admin, editor, and viewer `ClusterRole`s per CRD kind, for wiring up your own `RoleBinding`s. |

### `serviceAccount`

| Key | Default | Description |
|---|---|---|
| `serviceAccount.enable` | `true` | Create a ServiceAccount for the manager. Set `false` to bring your own, via `serviceAccount.name`. |
| `serviceAccount.name` | *(unset, falls back to the chart's fullname)* | Existing ServiceAccount name, only used when `enable` is `false`. |
| `serviceAccount.annotations`, `labels` | *(unset)* | |

### `crd`

| Key | Default | Description |
|---|---|---|
| `crd.enable` | `true` | Install the 12 CRDs as part of this release. Set `false` if you're managing them separately, for example via `kubectl apply -k config/crd` in a cluster where multiple releases share one CRD set. |
| `crd.keep` | `true` | Keep the CRDs, and therefore every CR, when you `helm uninstall`. Set `false` to let uninstall remove them too. This deletes every Polaris-side object every CR in the cluster manages, cascading through their finalizers. |

### `metrics`

| Key | Default | Description |
|---|---|---|
| `metrics.enable` | `true` | Expose the `/metrics` endpoint via a Service. |
| `metrics.port` | `8443` | |
| `metrics.secure` | `true` | `true` serves HTTPS with authentication (the manager's `ClusterRole` already includes metrics-auth access). `false` serves plain HTTP. |

### `certManager`

| Key | Default | Description |
|---|---|---|
| `certManager.enable` | `false` | Provision TLS certs via [cert-manager](https://cert-manager.io/) for webhook and metrics-endpoint certificates. This project has no webhooks today, since validation is CEL-only (see the [CRD reference](crds/index.md)), so this only matters if you want cert-manager-issued certs for the metrics endpoint instead of the built-in self-signed ones. |

### `prometheus`

| Key | Default | Description |
|---|---|---|
| `prometheus.enable` | `false` | Install a Prometheus Operator `ServiceMonitor` for the metrics endpoint. Requires the Prometheus Operator CRDs to already be installed. |

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
    Every `PolarisConnection`, `PolarisCatalog`, and so on that you want reconciled has to live in the same namespace as the release. For a cluster shared across multiple teams' namespaces, leave `rbac.namespaced` at its default.
