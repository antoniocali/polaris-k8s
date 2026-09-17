#!/usr/bin/env bash
# Apply local renames to the vendored Polaris OpenAPI specs so oapi-codegen
# produces unique Go identifiers. The vendored specs in openapi/ are kept
# verbatim from upstream; this script writes patched copies into bin/oapi/
# which are gitignored.
#
# Renames applied:
#   catalog plane:
#     - components.parameters.namespace -> nsParam
#       (collides with components.schemas.Namespace in Go: both map to
#        identifier "Namespace")
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${REPO_ROOT}/bin/oapi"
mkdir -p "${OUT}"

# Management plane: no patching needed today.
cp "${REPO_ROOT}/openapi/polaris-management-service.yaml" "${OUT}/polaris-management-service.yaml"

# Catalog plane: rename the namespace parameter component and update refs.
yq '
  .components.parameters.nsParam = .components.parameters.namespace
  | del(.components.parameters.namespace)
  | (.. | select(has("$ref")) | select(.["$ref"] == "#/components/parameters/namespace")).["$ref"] = "#/components/parameters/nsParam"
' "${REPO_ROOT}/openapi/polaris-catalog-service.yaml" > "${OUT}/polaris-catalog-service.yaml"

echo "Patched specs written to ${OUT}/"
