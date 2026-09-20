# Releasing

How a change on `main` turns into a tagged release, a container image, and a Helm chart.

## The four version axes

This project has four things that look like "the version" but move independently:

| Axis | Where it lives | Moves when |
|------|-----------------|------------|
| Release | git tag `vX.Y.Z`, GitHub Release | A release PR is merged |
| CRD API | `polaris.k8s.calific.io/v1alpha1` | A deliberate, rare API promotion (`v1alpha1` to `v1beta1`) |
| Helm chart | `dist/chart/Chart.yaml` `version` / `appVersion` | Kept in lockstep with the release tag |
| Apache Polaris compatibility | `openapi/README.md`, `Chart.yaml`'s `polaris-k8s.io/apache-polaris-version` annotation | An OpenAPI spec bump (see [CONTRIBUTING.md](CONTRIBUTING.md#updating-the-vendored-polaris-openapi-specs)) |

The CRD API version is intentionally not tied to the release tag. Standard Kubernetes convention: many releases can ship while the API stays `v1alpha1`. The Helm chart version, by contrast, is kept equal to the release tag because this is a single-app, single-chart repository; a separate chart version number would only be confusing.

## How a release happens

1. Merge PRs to `main` using [Conventional Commits](https://www.conventionalcommits.org/) prefixes (`feat:`, `fix:`, `docs:`, `chore:`, and so on). Every push to `main` runs [`release-please`](https://github.com/googleapis/release-please), which keeps a standing "Release vX.Y.Z" PR up to date, computing the next version from those prefixes and drafting `CHANGELOG.md`.
2. When you're ready to ship what's accumulated, review and merge the release PR. That merge:
   - Creates the git tag and the GitHub Release, with the changelog as its notes.
   - Bumps `dist/chart/Chart.yaml`'s `version` and `appVersion` to match, in the same PR.
3. The tag push triggers `.github/workflows/release.yml`, which:
   - Builds and pushes the manager image to `ghcr.io/antoniocali/polaris-k8s:<version>` (and `:latest`).
   - Packages the Helm chart and pushes it as an OCI artifact to `oci://ghcr.io/antoniocali/charts/polaris-k8s`, first patching the packaged copy's `manager.image.repository` from the source `values.yaml`'s placeholder (`controller`) to `ghcr.io/antoniocali/polaris-k8s`, so the published chart installs with no `--set` flags.
   - Appends the image tag, chart reference, and the Apache Polaris compatibility version to the GitHub Release notes.

Nothing here is silent: every version bump is a PR a human reviews and merges. The only fully automatic parts are computing what the next version *should* be, and publishing artifacts once a tag exists.

## Pre-1.0 status

Releases are `v0.x.y` while the CRD API is `v1alpha1`. `bump-minor-pre-major` is set in `release-please-config.json` so a `feat:` commit bumps the minor version (not the major) until a deliberate `1.0.0` cut.

## First-time GHCR package visibility

The first time an image or chart is pushed to a given GHCR package name, GitHub creates it as **private** by default, regardless of the repo's visibility. After the first release, go to the package settings on GitHub (org/user packages page) and set both `polaris-k8s` and `charts/polaris-k8s` to public so `docker pull` / `helm pull` work without authentication. This is a one-time step; subsequent pushes keep the visibility you set.

## Bootstrapping the first tag

`release-please` computes version bumps from Conventional Commit history since the last tag. Since this repo's existing history predates the Conventional Commits convention, there is nothing for it to compute a first bump from. The first release, `v0.1.0`, is a manual `gh release create v0.1.0` cutting the current `main` as the baseline; `release-please`'s manifest starts from that same `0.1.0` so it proposes the *next* version forward from there.
