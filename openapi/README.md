# Apache Polaris OpenAPI specs

Vendored specs from the upstream [`apache/polaris`](https://github.com/apache/polaris) project. The polaris-k8s HTTP client (under `internal/polaris/`) is built around these — Go types are generated from them, and the hand-written client facade calls the generated operations.

## Files

| File | Polaris API plane | Upstream path |
|------|-------------------|---------------|
| `polaris-management-service.yaml` | Management plane (catalogs, principals, roles, grants) | `spec/polaris-management-service.yml` |
| `polaris-catalog-service.yaml` | Catalog plane (namespaces, tables, views, policies — Iceberg REST API) | `spec/generated/bundled-polaris-catalog-service.yaml` |

## Pinned version

- **Apache Polaris**: `apache-polaris-1.4.1`
- **Vendored on**: 2026-05-13

## Updating

See [`CONTRIBUTING.md`](../CONTRIBUTING.md#updating-the-vendored-polaris-openapi-specs) for the exact commands. Always bump both files together — they belong to the same Polaris release.

After a bump:

1. Regenerate the Go client (`make polaris-client-gen`, once that target exists).
2. Run the full test suite (`make test`).
3. Note any breaking changes in the PR description; downstream consumers may need to update CR samples or reconciler logic.

## Why vendor rather than fetch at build time

- **Build reproducibility** — anyone building the operator gets the same client surface regardless of upstream availability.
- **Diffability** — a Polaris release bump is a reviewable PR diff, not an opaque CI dependency.
- **Air-gapped builds** — required for some deployment environments.

## License

The vendored specs are © Apache Software Foundation, licensed under Apache 2.0. See the upstream `LICENSE` file in `apache/polaris`.
