# Security Policy

## Supported Versions

`polaris-k8s` is alpha (pre-`v1beta1`). Only the latest commit on `main` is
supported; there are no maintained release branches yet.

## Reporting a Vulnerability

Please **do not** open a public GitHub Issue for security problems.

Instead, use one of:

- GitHub's [private vulnerability reporting](https://github.com/antoniocali/polaris-k8s/security/advisories/new)
  (Security tab → "Report a vulnerability").
- Email antoniodavidecali@gmail.com directly.

Please include steps to reproduce, the affected version/commit, and the
potential impact. I'll acknowledge reports within a few days.

PII-handling code paths are especially sensitive here: Polaris controls
access to Iceberg catalogs that may include masked-PII tables, so issues
affecting `PolarisGrant`/`PolarisPrincipal`/`PolarisCatalogRole` authorization
logic are treated as high priority.
