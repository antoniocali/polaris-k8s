# Troubleshooting

**`kubectl apply` rejected with a CEL message.** A cross-field invariant failed — e.g. `s3 must be set when storageType is S3`, or `target.namespaceRef is required when target.type is namespace`. The message names the offending field; fix the YAML and reapply. These are admission-time checks, so a broken CEL expression in the CRD itself would only ever surface here, never at `go build` time.

**`kubectl apply` rejected with an enum or pattern error.** Check the field against the marker constants in [`api/v1alpha1/*_types.go`](https://github.com/antoniocali/polaris-k8s/tree/main/api/v1alpha1) — e.g. `writeFormat` only accepts `parquet`/`orc`/`avro`, and most `name` fields are pattern-restricted to `^[a-zA-Z0-9_-]+$`.

**`spec.name` looks empty on a fresh CR.** That's expected. Defaulting to `.metadata.name` happens at reconcile time, not via a schema default, so the field stays empty in the stored object even though the Polaris-side resource is named after `.metadata.name`.

**A CR is stuck `Ready=False` with reason `DependencyNotReady`.** It's waiting on a parent — the condition `message` names which one (e.g. `waiting for PolarisConnection prod to be ready`). Check that parent's own `Ready` condition; the chain usually resolves itself once the root cause (often the `PolarisConnection`) is fixed. This is a quiet, short-interval requeue, not an error — you won't see it in the manager's error logs.

**A CR is stuck `Ready=False` with reason `AuthenticationError` or `PolarisError`.** `kubectl describe` it — the condition message carries the Polaris HTTP status and response body. Check the `PolarisConnection` is `Ready` first; everything chains off it.

**`PolarisPolicy` rejected by Polaris with a body-parsing error, even though it applied fine.** `spec.content` is validated by Polaris against a schema specific to `spec.type` that this CRD has no visibility into — a `kubectl apply`-time success only means the JSON is well-formed, not that it matches what that policy type expects. The error shows up as a `PolarisError` status condition, not an admission rejection.

**CRDs install but `kubectl explain` shows no schema.** `controller-runtime` (or your own client cache) cached the old schema. `kubectl delete crd <name>` and reapply.

**Nothing seems to be reconciling at all.** Check the manager pod itself:

```sh
kubectl -n polaris-k8s-system get pods
kubectl -n polaris-k8s-system logs deployment/polaris-k8s-controller-manager
```

and confirm it actually has RBAC for the kind in question — `kubectl auth can-i get polarisconnections --as=system:serviceaccount:polaris-k8s-system:polaris-k8s-controller-manager`.

Nothing here cover what you're seeing? Open an issue — see [Contributing](https://github.com/antoniocali/polaris-k8s/blob/main/CONTRIBUTING.md).
