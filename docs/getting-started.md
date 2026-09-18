# Getting started

Two paths, depending on what you want.

**Just want to see it work?** Skip everything below and run the local-dev harness. One command brings up a [kind](https://kind.sigs.k8s.io/) cluster, a real Apache Polaris in Docker (no cloud storage needed), the operator, and a full slice of the object graph:

```sh
git clone git@github.com:antoniocali/polaris-k8s.git
cd polaris-k8s
make local-up   # kind + Polaris + operator + sample CRs; Tilt UI at :10350
```

See [`hack/local-dev/README.md`](https://github.com/antoniocali/polaris-k8s/blob/main/hack/local-dev/README.md) for what it sets up and how to poke at it. Once it's up, jump to the [Tutorial](tutorial.md). Everything there works against this harness.

**Installing against a real cluster and a real Polaris server?** Follow the steps below.

## Prerequisites

- Kubernetes 1.27+. The CEL `x-kubernetes-validations` rules need 1.25+, and map-keyed list types need 1.27+ for correct server-side apply behavior.
- `kubectl` configured against the target cluster.
- A running Apache Polaris instance reachable from the cluster.
- `make` and `go` 1.26+, only if you're building the controller image yourself rather than using a published one.

## 1. Install the CRDs

From a checkout of this repo:

```sh
make manifests          # regenerate config/crd/bases/ from the Go types
kubectl apply -k config/crd
```

Verify:

```sh
kubectl api-resources --api-group=polaris.k8s.calific.io
```

You should see all 12 kinds. They share the `polaris` category, so `kubectl get polaris -A` returns every Polaris-managed object in the cluster, regardless of kind.

## 2. Deploy the controller

Two ways to do this. Same `config/` source underneath, pick whichever fits your workflow.

**Kustomize**, the same tool step 1 already used to install the CRDs:

```sh
make docker-build docker-push IMG=<registry>/polaris-k8s:<tag>
make deploy IMG=<registry>/polaris-k8s:<tag>
```

This installs the manager plus its RBAC into the `polaris-k8s-system` namespace. To remove it, run `make undeploy`.

**Helm** installs CRDs and the controller together in one command, and skips step 1 entirely:

```sh
make helm-deploy IMG=<registry>/polaris-k8s:<tag>
```

or directly:

```sh
helm upgrade --install polaris-k8s ./dist/chart \
  --namespace polaris-k8s-system --create-namespace \
  --set manager.image.repository=<registry>/polaris-k8s \
  --set manager.image.tag=<tag> \
  --wait
```

See the [CRD reference](crds/index.md) for what to apply next either way, or the [Helm chart reference](helm-chart.md) for every configuration knob (RBAC scope, metrics, resource limits). Remove with `make helm-uninstall`.

## 3. Point it at your Polaris server

Everything from here is a `PolarisConnection`, and whatever you build on top of it. Covered step by step in the [Tutorial](tutorial.md).

## Uninstalling

```sh
kubectl delete -k config/crd     # removes the CRDs and cascades deletion of every CR
make undeploy                    # removes the controller deployment and RBAC
```

Every CR carries a `polaris.k8s.calific.io/finalizer`, so deleting a CR deletes the corresponding Polaris-side object first, then removes the Kubernetes record. Deleting the CRDs themselves skips that step. If you want the Polaris-side objects cleaned up too, prefer `kubectl delete` of individual CRs, or whole namespaces, while the controller is still running.
