# Why polaris-k8s

[Apache Polaris](https://polaris.apache.org/) is a REST catalog for [Apache Iceberg](https://iceberg.apache.org/) — it tracks catalogs, namespaces, tables, views, and the principals/roles/grants that control access to them. Out of the box, the only way to configure any of that is Polaris's own REST API: `curl` scripts, a hand-rolled client, or click-ops in an ad-hoc admin tool.

That doesn't fit a GitOps workflow. There's no review step before a new principal gets `CATALOG_MANAGE_ACCESS`, no audit trail for who created a catalog or when, no diff before a grant changes, and no way to reconstruct "what did our access model look like last quarter" beyond digging through Polaris's own history (if it kept one).

`polaris-k8s` turns Polaris configuration into Kubernetes Custom Resources. The same catalog, namespace, principal, role, and grant objects Polaris manages server-side become YAML files in a Git repo, reconciled continuously by a controller running in your cluster.

## What that buys you

- **Reviewable changes.** A new catalog, a schema change, a privilege grant — all of it goes through a pull request like any other infrastructure change, instead of a `curl -X POST` someone ran once and nobody wrote down.
- **An audit trail for free.** `git log` on your CR manifests *is* the history of who changed access to what, and when — no separate system to stand up.
- **Standard tooling.** `kubectl get polaris -A` lists every Polaris-side object in the cluster. `kubectl describe` on any of them shows you exactly why it isn't `Ready` yet. Anything that already watches Kubernetes events, metrics, or audit logs works here too.
- **Composable with the rest of your platform.** Reference a `PolarisPrincipal`'s generated credentials `Secret` from a Job or a Deployment the same way you'd reference any other Secret. The catalog's access model lives next to the workloads that use it.

## What it isn't

`polaris-k8s` **reconciles Kubernetes objects into Polaris — not the other way around.** It has no discovery loop: a table created directly against Polaris by Spark, Flink, or any other Iceberg client has no corresponding CR and never will, unless someone later applies one with a matching name (at which point the operator adopts it rather than erroring). This isn't a bidirectional sync, and it isn't a Polaris server itself — see the [official Apache Polaris Helm chart](https://artifacthub.io/packages/helm/apache-polaris/polaris) for that. The two are complementary: run Polaris however you already do, then point this operator at it with a `PolarisConnection`.

It's also worth being honest about scope. The kinds that map cleanly onto "infrastructure a platform team manages via GitOps" — connections, catalogs, namespaces, principals, roles, bindings, grants — are a strong fit: they change rarely and genuinely benefit from PR review. `PolarisTable` and `PolarisView` are a weaker fit for *ongoing* schema management — in practice, table schemas evolve through the data engine itself (Spark DDL, dbt migrations), not through a platform team's GitOps loop, which is why schema/partition drift on tables and SQL drift on views aren't reconciled today (initial create is full-fidelity; further changes need a manual drop+recreate). They're most useful for *bootstrapping* a namespace's expected tables, not for driving their day-to-day evolution.

## Status

**Alpha.** All 12 reconcilers are implemented, unit-tested against a fake Polaris server, covered by an envtest suite against a real Kubernetes API server, and proven end-to-end against a real Apache Polaris instance. The API is still `v1alpha1` — it may change without a conversion webhook, so pin to a release tag in production.

Ready to try it? Head to [Getting started](getting-started.md), or jump straight into the [Tutorial](tutorial.md) to see the whole object graph reconcile end to end.
