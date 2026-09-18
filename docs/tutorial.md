# Tutorial: build a full object graph

This walks through the same object graph the project's own end-to-end test exercises against a real Apache Polaris server. A connection, a catalog with a table and a view in it, and an identity that's been granted write access. Twelve CRDs, wired together, reconciled to `Ready` one dependency layer at a time.

Assumes you've already [installed the CRDs and deployed the controller](getting-started.md), or are running the [local-dev harness](getting-started.md) (`make local-up`), which already has a namespace, a connection, and Tilt watching everything for you.

Every example below uses namespace `data-platform`. Swap in your own.

## 1. Connect to Polaris

The operator authenticates to Polaris with OAuth client credentials. Create a Secret holding them, then a `PolarisConnection` pointing at it:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: polaris-admin-credentials
  namespace: data-platform
type: Opaque
stringData:
  clientId: "<oauth-client-id>"
  clientSecret: "<oauth-client-secret>"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisConnection
metadata:
  name: prod
  namespace: data-platform
spec:
  serverUrl: https://polaris.internal.example.com
  credentialsSecretRef:
    name: polaris-admin-credentials
```

```sh
kubectl apply -f connection.yaml
kubectl get polarisconnection prod -n data-platform
```

```
NAME   SERVER                              READY   AGE
prod   https://polaris.internal.example.com   True    5s
```

`Ready=True` here means the operator successfully exchanged the credentials for an access token. Every other resource you create will refuse to do anything until its connection is `Ready`, either directly or through a parent. Check `.status.conditions` for a `DependencyNotReady` reason if something seems stuck.

## 2. Create a catalog

A `PolarisCatalog` is the top-level container. It's the parent of every namespace, table, view, and catalog-scoped role you'll create.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalog
metadata:
  name: lakehouse
  namespace: data-platform
spec:
  connectionRef:
    name: prod
  defaultBaseLocation: s3://my-lakehouse/catalogs/lakehouse
  storageConfig:
    storageType: S3
    allowedLocations: [s3://my-lakehouse/catalogs/lakehouse]
    s3:
      roleArn: arn:aws:iam::123456789012:role/polaris
      region: eu-west-1
```

```sh
kubectl apply -f catalog.yaml
kubectl get polariscatalog lakehouse -n data-platform
```

## 3. Add a namespace

Namespaces nest by chaining `parentRef` rather than carrying a full path. Each level is its own CR with its own status.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisNamespace
metadata:
  name: analytics
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse
```

```sh
kubectl apply -f namespace.yaml
kubectl get polarisnamespace analytics -n data-platform
```

```
NAME        CATALOG     PATH            READY   AGE
analytics   lakehouse   ["analytics"]   True    3s
```

## 4. Create a table and a view

Both live inside the namespace and share the same Iceberg schema shape.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisTable
metadata:
  name: orders
  namespace: data-platform
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
        required: true
      - id: 2
        name: amount
        type: "decimal(10,2)"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisView
metadata:
  name: orders-summary
  namespace: data-platform
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
  sql: "SELECT id FROM analytics.orders"
```

```sh
kubectl apply -f table.yaml -f view.yaml
kubectl get polaristable,polarisview -n data-platform
```

!!! note
    Schema and partition-spec changes on an existing table aren't reconciled after creation, and neither is SQL drift on a view. See [Why](index.md) for the reasoning. The initial create is full-fidelity; changing the schema later needs a manual drop and recreate.

## 5. Create an identity

A `PolarisPrincipal` is a service or user identity. The operator generates its credentials and writes them into a Secret you name. You never set a password yourself.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipal
metadata:
  name: airflow-worker
  namespace: data-platform
spec:
  connectionRef:
    name: prod
  credentialsSecretRef:
    name: airflow-worker-polaris-creds
```

```sh
kubectl apply -f principal.yaml
kubectl get secret airflow-worker-polaris-creds -n data-platform -o jsonpath='{.data.clientId}' | base64 -d
```

That Secret is owned by the `PolarisPrincipal` CR, with a real Kubernetes owner reference, so it's garbage-collected when the principal is deleted. Mount it into whatever workload needs to authenticate to Polaris as this identity, the same way you'd mount any other Secret.

## 6. Wire up roles, bindings, and a grant

Access assembles in three parts. First a principal role and a catalog role. Then bindings that connect a principal to its principal role, and that principal role to a catalog role. Finally a grant that attaches a privilege to the catalog role.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRole
metadata:
  name: analytics-writer
  namespace: data-platform
spec:
  connectionRef:
    name: prod
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRole
metadata:
  name: lakehouse-analytics-rw
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRoleBinding
metadata:
  name: airflow-worker-as-analytics-writer
  namespace: data-platform
spec:
  principalRef:
    name: airflow-worker
  principalRoleRef:
    name: analytics-writer
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRoleBinding
metadata:
  name: analytics-writer-to-lakehouse-rw
  namespace: data-platform
spec:
  principalRoleRef:
    name: analytics-writer
  catalogRoleRef:
    name: lakehouse-analytics-rw
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisGrant
metadata:
  name: analytics-rw-table-write
  namespace: data-platform
spec:
  catalogRoleRef:
    name: lakehouse-analytics-rw
  privilege: TABLE_WRITE_DATA
  target:
    type: namespace
    namespaceRef:
      name: analytics
```

```sh
kubectl apply -f roles-and-grant.yaml
kubectl get polaris -n data-platform
```

Once everything settles, `airflow-worker` can write to any table under the `analytics` namespace. Every step of how it got that access is a commit in your Git history.

## 7. Watch it all together

```sh
kubectl get polaris -n data-platform
```

Every CRD shares the `polaris` category, so this returns every kind at once: connection, catalog, namespace, table, view, principal, both roles, both bindings, and the grant, each with a `Ready` column.

For any object stuck `Ready=False`, describe it:

```sh
kubectl describe <kind> <name> -n data-platform
```

The condition message names exactly what it's waiting on, or what Polaris returned. See [Troubleshooting](troubleshooting.md) for the common ones.

## Tearing it down

```sh
kubectl delete ns data-platform
```

Deleting the namespace cascades through every CR's finalizer. Each Polaris-side object is deleted before its Kubernetes record disappears, so nothing gets orphaned in Polaris: no catalogs, tables, or principals left behind.
