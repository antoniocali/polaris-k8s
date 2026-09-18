# Troubleshooting

**`kubectl apply` rejected with a CEL message.** A cross-field invariant failed. For example, `s3 must be set when storageType is S3`, or `target.namespaceRef is required when target.type is namespace`. The message names the offending field, so fix the YAML and reapply. These are admission-time checks. A broken CEL expression in the CRD itself would only ever surface here, never at build time.

**`kubectl apply` rejected with an enum or pattern error.** Check the field against the marker constants in [`api/v1alpha1/*_types.go`](https://github.com/antoniocali/polaris-k8s/tree/main/api/v1alpha1). For example, `writeFormat` only accepts `parquet`, `orc`, or `avro`, and most name fields are pattern-restricted to lowercase alphanumerics, `-`, and `_`.

**`spec.name` looks empty on a fresh CR.** That's expected. Defaulting to the object's own name happens at reconcile time, not through a schema default, so the field stays empty in the stored object even though the Polaris-side resource is already named after it.

**A CR is stuck `Ready=False` with reason `DependencyNotReady`.** It's waiting on a parent. The condition message names which one, for example `waiting for PolarisConnection prod to be ready`. Check that parent's own `Ready` condition. The chain usually resolves itself once the root cause, often the connection, is fixed. This is a quiet, short-interval requeue, not an error, so you won't see it in the manager's error logs.

**A CR is stuck `Ready=False` with reason `AuthenticationError` or `PolarisError`.** Describe it. The condition message carries the Polaris HTTP status and response body. Check that the connection is `Ready` first, since everything chains off it.

**`PolarisPolicy` gets rejected by Polaris with a body-parsing error, even though it applied fine.** `spec.content` is validated by Polaris against a schema specific to `spec.type`, and this CRD has no visibility into that schema. A successful `kubectl apply` only means the JSON is well-formed, not that it matches what that policy type expects. The error shows up as a `PolarisError` status condition, not as an admission rejection.

**CRDs install but `kubectl explain` shows no schema.** Something (controller-runtime, or your own client) cached the old schema. Delete the CRD and reapply it.

**Nothing seems to be reconciling at all.** Check the manager pod itself:

```sh
kubectl -n polaris-k8s-system get pods
kubectl -n polaris-k8s-system logs deployment/polaris-k8s-controller-manager
```

Then confirm it actually has RBAC for the kind in question:

```sh
kubectl auth can-i get polarisconnections \
  --as=system:serviceaccount:polaris-k8s-system:polaris-k8s-controller-manager
```

Still stuck? Open an issue. See [Contributing](https://github.com/antoniocali/polaris-k8s/blob/main/CONTRIBUTING.md).
