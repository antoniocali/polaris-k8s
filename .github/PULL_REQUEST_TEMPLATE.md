## What changed

## Why

## How it was tested

- [ ] `make fmt vet`
- [ ] `go test ./api/... -count=1`
- [ ] `make test` (full suite incl. envtest)
- [ ] `make manifests generate` run and committed, if `api/v1alpha1/*_types.go` changed

## Checklist

- [ ] One logical change (see [CONTRIBUTING.md](../CONTRIBUTING.md))
- [ ] New/changed behavior has test coverage
- [ ] Docs updated (`README.md`, `docs/`, `CLAUDE.md`) if applicable
