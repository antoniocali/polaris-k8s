# PolarisCatalog

A top-level Polaris catalog, the parent of every namespace, table, view, and catalog-scoped role. Owns the catalog's storage backend configuration.

## Spec

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `connectionRef` | [ConnectionRef](index.md#refs) | yes | | The `PolarisConnection` this catalog lives on. |
| `name` | string | no | `.metadata.name` | Catalog name in Polaris. Pattern `^[a-zA-Z0-9_-]+$`. |
| `type` | `INTERNAL` \| `EXTERNAL` | no | `INTERNAL` | Whether Polaris owns catalog state, or federates from an external catalog. |
| `defaultBaseLocation` | string | yes | | Storage URI under which new tables land unless overridden. |
| `storageConfig` | [StorageConfig](#storageconfig) | yes | | Cloud storage backend configuration. |
| `properties` | map[string]string | no | | Open-ended property bag Polaris associates with the catalog. |

### StorageConfig

The `s3`, `azure`, or `gcs` sub-block matching `storageType` is required. A CEL rule enforces this, so `kubectl apply` fails with a clear message if it's missing. `FILE` storage needs none of them, just `allowedLocations`. It's for testing only, and Polaris itself rejects it unless the server was started with `ALLOW_INSECURE_STORAGE_TYPES=true`.

| Field | Type | Required | Description |
|---|---|---|---|
| `storageType` | `S3` \| `AZURE` \| `GCS` \| `FILE` | yes | Which cloud backend the catalog data lives in. |
| `allowedLocations` | []string | yes (≥1) | URI prefixes Polaris may read/write. |
| `s3.roleArn` | string | if S3 | IAM role Polaris assumes to access S3. |
| `s3.region` | string | if S3 | AWS region of the bucket. |
| `s3.externalId` | string | no | Required on the role's trust policy, if set. |
| `s3.userArn` | string | no | ARN Polaris itself runs as, for trust-policy setup. |
| `azure.tenantId` | string | if Azure | Azure AD tenant ID Polaris federates with. |
| `azure.multiTenantAppName` | string | no | Azure AD multi-tenant application name. |
| `azure.consentUrl` | string | no | Azure consent URL for admin consent flows. |
| `gcs.gcsServiceAccount` | string | if GCS | GCP service account email Polaris impersonates. |

## Status

| Field | Description |
|---|---|
| `polarisCatalogId` | Server-side identifier for the catalog. |
| `conditions` | `Ready`, `Synced`. |

## Drift policy

`properties` is **authoritative**. Keys present in the Polaris-side catalog but not in `spec.properties` are removed on the next reconcile. Everything else about the catalog updates in place, except `storageConfig` and `defaultBaseLocation`, which Polaris itself doesn't support changing after creation.

## Example

```yaml
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisCatalog
metadata:
  name: lakehouse
  namespace: data-platform
spec:
  connectionRef:
    name: prod
  defaultBaseLocation: s3://my-lakehouse/catalogs/lakehouse
  storageConfig:
    storageType: S3
    allowedLocations: [s3://my-lakehouse/catalogs/lakehouse]
    s3:
      roleArn: arn:aws:iam::123456789012:role/polaris
      region: eu-west-1
```
