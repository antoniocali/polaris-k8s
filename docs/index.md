# Why polaris-k8s

[Apache Polaris](https://polaris.apache.org/) is a REST catalog for [Apache Iceberg](https://iceberg.apache.org/). It tracks catalogs, namespaces, tables, views, and the principals, roles, and grants that control access to them. Out of the box, the only way to configure any of that is Polaris's own REST API. That means curl scripts, a hand-rolled client, or click-ops in an ad-hoc admin tool.

That doesn't fit a GitOps workflow. There's no review step before a new principal gets elevated access. There's no audit trail for who created a catalog, or when. There's no diff before a grant changes. And there's no way to reconstruct what your access model looked like last quarter, beyond digging through Polaris's own history, if it kept one.

`polaris-k8s` turns Polaris configuration into Kubernetes Custom Resources. The same catalog, namespace, principal, role, and grant objects Polaris manages server-side become YAML files in a Git repo, reconciled continuously by a controller running in your cluster.

## What that buys you

- **Reviewable changes.** A new catalog, a schema change, a privilege grant: all of it goes through a pull request like any other infrastructure change. Not a one-off API call nobody wrote down.
- **An audit trail for free.** `git log` on your CR manifests is the history of who changed access to what, and when. No separate system to stand up.
- **Standard tooling.** `kubectl get polaris -A` lists every Polaris-side object in the cluster. `kubectl describe` on any of them shows you exactly why it isn't `Ready` yet. Anything that already watches Kubernetes events, metrics, or audit logs works here too.
- **Composable with the rest of your platform.** Reference a `PolarisPrincipal`'s generated credentials Secret from a Job or a Deployment the same way you'd reference any other Secret. The catalog's access model lives next to the workloads that use it.

!!! note "Not the same as the official Apache Polaris Helm chart"
    The [official chart](https://artifacthub.io/packages/helm/apache-polaris/polaris) deploys the Polaris server itself. This operator deploys a controller that reconciles Polaris resources against an already-running server: catalogs, namespaces, principals, roles, grants. The two are complementary. Run Polaris however you already do, then point this operator at it with a `PolarisConnection`.

## What it isn't

`polaris-k8s` reconciles Kubernetes objects into Polaris, not the other way around. It has no discovery loop. A table created directly against Polaris by Spark, Flink, or any other Iceberg client has no corresponding CR, and never will on its own. If someone later applies a CR with a matching name, the operator adopts it instead of erroring, but that only happens when asked.

It's also worth being honest about scope. Connections, catalogs, namespaces, principals, roles, bindings, and grants map cleanly onto infrastructure a platform team manages through GitOps. They change rarely, and they genuinely benefit from PR review.

Tables and views are a weaker fit for ongoing schema management. In practice, table schemas evolve through the data engine itself, through Spark DDL or dbt migrations, not through a platform team's GitOps loop. That's also why schema and partition drift on tables, and SQL drift on views, aren't reconciled: the initial create is full-fidelity, but further changes need a manual drop and recreate. Tables and views are most useful for bootstrapping a namespace's expected structure, not for driving its day-to-day evolution.

## Status

**Alpha.** All 12 reconcilers are implemented. They're unit-tested against a fake Polaris server, covered by an envtest suite against a real Kubernetes API server, and proven end to end against a real Apache Polaris instance. The API is still `v1alpha1`. It may change without a conversion webhook, so pin to a release tag in production. One concrete gap: there's no `spec.deletionPolicy` field yet, so deleting a CR always cascades to the Polaris-side object, with no way to orphan it instead.

Ready to try it? Head to [Getting started](getting-started.md), or jump straight into the [Tutorial](tutorial.md) to see the whole object graph reconcile end to end.
