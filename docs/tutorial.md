# Tutorial: build a full object graph

This walks through the same object graph the project's own end-to-end test exercises against a real Apache Polaris server. A connection, a catalog with a table and a view in it, and an identity that's been granted write access. Twelve CRDs, wired together, reconciled to `Ready` one dependency layer at a time.

Most code blocks below have small numbered markers next to the interesting lines. Click one to see what that specific line does and, where it matters, what it actually causes in Polaris itself. This project doesn't try to be a full Polaris reference, but the concepts matter for understanding what you're applying, so we'll cover just enough of them along the way.

Assumes you've already [installed the CRDs and deployed the controller](getting-started.md), or are running the [local-dev harness](getting-started.md) (`make local-up`), which already has a namespace, a connection, and Tilt watching everything for you.

Every example below uses namespace `data-platform`. Swap in your own.

## 1. Connect to Polaris

A `PolarisConnection` is the operator's own handle to a Polaris server. It's worth being clear about this up front: **Polaris itself has no concept of a "connection".** Nothing gets created server-side when you apply one. Reconciling it just means checking that the referenced Secret exists and using it to mint an OAuth token, to prove the credentials actually work before anything downstream tries to use them.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: polaris-admin-credentials
  namespace: data-platform
type: Opaque
stringData:
  clientId: "<oauth-client-id>" # (1)!
  clientSecret: "<oauth-client-secret>"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisConnection
metadata:
  name: prod
  namespace: data-platform
spec:
  serverUrl: https://polaris.internal.example.com # (2)!
  credentialsSecretRef:
    name: polaris-admin-credentials # (3)!
```

1. Polaris authenticates over OAuth2 client credentials, the same flow a service account uses to talk to most REST APIs. `clientId`/`clientSecret` is the pair Polaris issued when this identity (often a root or admin principal) was created. The operator never generates or stores these itself for a `PolarisConnection`, you provide them.
2. The base URL of your Polaris server. The operator appends the token endpoint path to this (`/api/catalog/v1/oauth/tokens` by default) to exchange the credentials above for a short-lived bearer token, and appends the management/catalog API paths for everything else it does.
3. Points back at the Secret. The operator re-reads it and re-authenticates whenever the cached token expires, so rotating the Secret's contents is enough to rotate what the operator authenticates as. It never writes to this Secret; that pattern is reserved for `PolarisPrincipal`, covered in [step 5](#5-create-an-identity).

```sh
kubectl apply -f connection.yaml
kubectl get polarisconnection prod -n data-platform
```

```
NAME   SERVER                              READY   AGE
prod   https://polaris.internal.example.com   True    5s
```

`Ready=True` here means the operator successfully exchanged the credentials for an access token, nothing more. Every other resource you create will refuse to do anything until its connection is `Ready`, either directly or through a parent. Check `.status.conditions` for a `DependencyNotReady` reason if something seems stuck.

## 2. Create a catalog

A `PolarisCatalog` is the top-level container in Polaris: a named collection of namespaces, tables, and views, backed by one storage location. This is the first object in the tutorial that actually creates something server-side. Applying it makes the operator call Polaris's management API and provision a real catalog.

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalog
metadata:
  name: lakehouse
  namespace: data-platform
spec:
  connectionRef:
    name: prod
  defaultBaseLocation: s3://my-lakehouse/catalogs/lakehouse # (1)!
  storageConfig:
    storageType: S3
    allowedLocations: [s3://my-lakehouse/catalogs/lakehouse] # (2)!
    s3:
      roleArn: arn:aws:iam::123456789012:role/polaris # (3)!
      region: eu-west-1
```

1. Where Iceberg table data physically lands by default. Every table you create under this catalog writes its data files somewhere under this prefix, unless a table overrides it.
2. Polaris enforces this as an allow-list. Any location a table or namespace under this catalog tries to write to has to fall under one of these prefixes, which is Polaris's own guardrail against a catalog reading or writing storage it wasn't meant to touch.
3. The IAM role Polaris assumes to actually read and write S3 on this catalog's behalf. Polaris doesn't use your own AWS credentials; it federates through this role, so the role's trust policy has to allow Polaris's own identity to assume it.

```sh
kubectl apply -f catalog.yaml
kubectl get polariscatalog lakehouse -n data-platform
```

## 3. Add a namespace

A namespace in Polaris (and in Iceberg generally) is a logical grouping inside a catalog, roughly analogous to a schema in a traditional database. Namespaces nest by chaining `parentRef` rather than carrying a full path, so each level is its own CR with its own status and its own lifecycle.

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

The `PATH` column is what Polaris actually stores. For a nested namespace it would show something like `["analytics", "sales"]`; the [namespace reference](crds/namespace.md) has a worked nesting example if you need that.

## 4. Create a table and a view

Both live inside the namespace and share the same Iceberg schema shape: a list of typed, ID-numbered fields. The ID matters more than it might look like it should. Iceberg tracks columns by their numeric ID internally, not by name, which is exactly what lets you rename a column later without breaking anything reading old data files. This project doesn't reconcile schema changes after creation (see the note below), but the ID is still part of the real Iceberg schema Polaris stores.

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
      - id: 1 # (1)!
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
  sql: "SELECT id FROM analytics.orders" # (2)!
```

1. Iceberg's internal, stable field ID. Two columns with the same name in two different table versions are only "the same column" to Iceberg if they share this ID.
2. Views in Polaris store the query text itself, not materialized data. Reading from `orders-summary` runs this SQL against whatever engine you query it from (Spark, Trino, and so on); Polaris just tracks the definition and the schema it's expected to produce.

```sh
kubectl apply -f table.yaml -f view.yaml
kubectl get polaristable,polarisview -n data-platform
```

!!! note
    Schema and partition-spec changes on an existing table aren't reconciled after creation, and neither is SQL drift on a view. See [Why](index.md) for the reasoning. The initial create is full-fidelity; changing the schema later needs a manual drop and recreate.

## 5. Create an identity

A `PolarisPrincipal` is a service or user identity Polaris can authenticate as, distinct from the admin identity your `PolarisConnection` uses. This is the one place the operator generates a secret rather than consuming one: it calls Polaris to create the principal, Polaris returns a fresh `clientId`/`clientSecret`, and the operator writes that pair into a Secret you name.

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
    name: airflow-worker-polaris-creds # (1)!
```

1. This Secret doesn't exist yet; you're not referencing one you created, you're naming the one the operator is about to create and own. It carries a real Kubernetes owner reference back to this `PolarisPrincipal`, so deleting the principal deletes the Secret too.

```sh
kubectl apply -f principal.yaml
kubectl get secret airflow-worker-polaris-creds -n data-platform -o jsonpath='{.data.clientId}' | base64 -d
```

At this point `airflow-worker` exists in Polaris as an identity, but it can't do anything yet. Creating a principal grants it no privileges at all; that's what the rest of this tutorial builds toward.

## 6. Wire up roles, bindings, and a grant

This is the part that trips people up, so it's worth slowing down for the model before applying anything.

### The model

Polaris never lets you grant a privilege directly to a principal. Instead, access flows through two levels of indirection:

- A **principal role** is a reusable label for "what job does this identity do", independent of any one catalog. Think `analytics-writer`, not tied to `lakehouse` specifically.
- A **catalog role** is a reusable label for "what can you do in this one catalog", independent of who holds it. Think `lakehouse-analytics-rw`, not tied to any one team.
- A **binding** connects the two: which principal roles inherit which catalog roles, and separately, which principals hold which principal roles.
- A **grant** is what actually attaches a privilege, like "write table data", to a catalog role.

The payoff for this indirection: if you later add a second catalog, you reuse the same `analytics-writer` principal role and just bind it to that catalog's own catalog role. If you onboard a second team that needs the same access, you bind their principal role to your existing `lakehouse-analytics-rw` catalog role instead of re-granting the same privileges again. Nobody's individual access is a special case; it's always "which roles do you hold", the same shape as IAM roles in AWS or Google Cloud, or RBAC role bindings in Kubernetes itself.

Concretely, for `airflow-worker` to write to the `analytics` namespace, five objects have to exist:

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRole
metadata:
  name: analytics-writer
  namespace: data-platform
spec:
  connectionRef:
    name: prod # (1)!
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalogRole
metadata:
  name: lakehouse-analytics-rw
  namespace: data-platform
spec:
  catalogRef:
    name: lakehouse # (2)!
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisPrincipalRoleBinding
metadata:
  name: airflow-worker-as-analytics-writer
  namespace: data-platform
spec:
  principalRef:
    name: airflow-worker # (3)!
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
    name: analytics-writer # (4)!
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
    name: lakehouse-analytics-rw # (5)!
  privilege: TABLE_WRITE_DATA
  target:
    type: namespace
    namespaceRef:
      name: analytics
```

1. A principal role is server-wide, not scoped to a catalog, which is why it references a `PolarisConnection` rather than a `PolarisCatalog`. On its own this creates the role in Polaris and grants nothing.
2. A catalog role is scoped to exactly one catalog, `lakehouse` here. On its own this also grants nothing; it's just a named bucket you're about to attach a privilege to.
3. This is the first of the two bindings: it says the `airflow-worker` principal holds the `analytics-writer` principal role. Without it, `airflow-worker` would hold no roles at all, no matter what those roles could do.
4. The second binding: it says the `analytics-writer` principal role inherits whatever the `lakehouse-analytics-rw` catalog role can do. This is the step that actually connects the principal side to the catalog side.
5. This is the only object so far that grants an actual privilege. Everything before it was plumbing; this is the payoff. `TABLE_WRITE_DATA` on `target.type: namespace` means write access to every table under the `analytics` namespace, not just one table, current and future.

```sh
kubectl apply -f roles-and-grant.yaml
kubectl get polaris -n data-platform
```

### What just happened

Follow the chain in order. The `PolarisPrincipalRoleBinding` says `airflow-worker` holds `analytics-writer`. The `PolarisCatalogRoleBinding` says `analytics-writer` inherits `lakehouse-analytics-rw`. The `PolarisGrant` says `lakehouse-analytics-rw` can write table data under `analytics`. Chain all three together and `airflow-worker` can now write to any table under `analytics`, present or future. Every step of how it got that access, and why, is a commit in your Git history.

If you only take one thing from this section: **a missing binding is the single most common reason a principal "should" have access but doesn't.** The principal role and the catalog role can both be `Ready`, the grant can be `Ready`, and access still won't work if nothing binds them together. Check `kubectl get polarisprincipalrolebinding,polariscatalogrolebinding -n data-platform` first when access isn't behaving the way you expect.

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
