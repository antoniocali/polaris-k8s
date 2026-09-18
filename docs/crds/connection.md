# PolarisConnection

A handle to an Apache Polaris server: its URL and the OAuth client credentials the operator authenticates with. Every other resource in this API group attaches to a `PolarisConnection`, either directly or through a parent that does. It owns no Polaris-side state. Reconciling one means validating the referenced Secret and minting a token to prove the credentials actually work.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `serverUrl` | string | yes | | Base URL of the Polaris server, e.g. `https://polaris.internal`. |
| `tokenPath` | string | no | `/api/catalog/v1/oauth/tokens` | OAuth token endpoint path appended to `serverUrl`. |
| `scope` | string | no | `PRINCIPAL_ROLE:ALL` | OAuth scope requested when minting tokens. |
| `credentialsSecretRef` | [ClientCredentialsSecretRef](#clientcredentialssecretref) | yes | | Secret containing the OAuth `clientId`/`clientSecret`. |
| `caBundleSecretRef` | [CABundleSecretRef](#cabundlesecretref) | no | | Secret with a PEM CA bundle, for self-signed servers. |
| `insecureSkipVerify` | bool | no | `false` | Disables TLS verification. Discouraged outside local dev. |

### ClientCredentialsSecretRef

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | | Same-namespace Secret name. |
| `clientIdKey` | string | no | `clientId` | Key in the Secret holding the OAuth client ID. |
| `clientSecretKey` | string | no | `clientSecret` | Key in the Secret holding the OAuth client secret. |

### CABundleSecretRef

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | | Same-namespace Secret name. |
| `key` | string | no | `ca.crt` | Key in the Secret holding the PEM CA bundle. |

## Status

| Field | Description |
|---|---|
| `serverVersion` | The Polaris server version, if discoverable. |
| `conditions` | `Ready`, `AuthValid`. |

## Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: polaris-admin-credentials
  namespace: data-platform
type: Opaque
stringData:
  clientId: "<oauth-client-id>"
  clientSecret: "<oauth-client-secret>"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisConnection
metadata:
  name: prod
  namespace: data-platform
spec:
  serverUrl: https://polaris.internal.example.com
  credentialsSecretRef:
    name: polaris-admin-credentials
```

For a self-signed server:

```yaml
spec:
  caBundleSecretRef:
    name: polaris-ca
    key: ca.crt   # default
```
