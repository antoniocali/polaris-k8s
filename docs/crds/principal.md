# PolarisPrincipal

An identity (user or service) registered in Polaris. The operator manages its lifecycle and writes the generated `clientId`/`clientSecret` to a Secret you name — you never set credentials yourself.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `connectionRef` | [ConnectionRef](index.md#refs) | yes | | The `PolarisConnection` this principal lives on. |
| `name` | string | no | `.metadata.name` | Principal name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `credentialRotationRequired` | bool | no | `false` | Set to `true` to force the operator to rotate `clientSecret` on the next reconcile. |
| `properties` | map[string]string | no | | Open-ended property bag (e.g. `department`, `owner-team`). |
| `credentialsSecretRef` | [GeneratedCredentialsSecretRef](#generatedcredentialssecretref) | yes | | Where the operator writes the generated credentials. |

### GeneratedCredentialsSecretRef

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Same-namespace Secret name. The operator creates and owns this Secret (real `ownerReferences` — garbage-collected when the principal is deleted). |

## Status

| Field | Description |
|---|---|
| `principalId` | Server-side identifier for the principal. |
| `credentialsLastRotated` | When the operator last wrote a new `clientSecret`. |
| `conditions` | `Ready`, `CredentialsWritten`. |

## Example

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
kubectl get secret airflow-worker-polaris-creds -n data-platform \
  -o jsonpath='{.data.clientId}' | base64 -d
```

## Rotating credentials

```sh
kubectl patch polarisprincipal airflow-worker -n data-platform \
  --type merge -p '{"spec":{"credentialRotationRequired":true}}'
```

The operator rotates the secret on its next reconcile and resets the field. Consumers reading the Secret at pod start (rather than caching the value) pick up the new credentials on their next restart.
