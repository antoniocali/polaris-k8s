#!/usr/bin/env bash
# Authenticated curl against the local Polaris REST API.
#
# Handles the OAuth token exchange for you, then issues the request and
# pretty-prints JSON. The closest thing to a "Polaris UI" — Polaris has no web
# console, so this is how you browse/poke the server by hand.
#
# Usage:
#   hack/local-dev/polaris-api.sh GET  /api/management/v1/catalogs
#   hack/local-dev/polaris-api.sh GET  /api/management/v1/catalogs/lakehouse
#   hack/local-dev/polaris-api.sh GET  /api/management/v1/principals
#   hack/local-dev/polaris-api.sh GET  /api/catalog/v1/lakehouse/namespaces
#   hack/local-dev/polaris-api.sh POST /api/management/v1/catalogs -d '{...}'
#
# Honors POLARIS_URL / POLARIS_CLIENT_ID / POLARIS_CLIENT_SECRET (see env.sh).
set -euo pipefail

url="${POLARIS_URL:-http://localhost:8181}"
cid="${POLARIS_CLIENT_ID:-root}"
csecret="${POLARIS_CLIENT_SECRET:-s3cr3t}"

method="${1:?usage: polaris-api.sh METHOD PATH [extra curl args]}"
endpoint="${2:?usage: polaris-api.sh METHOD PATH [extra curl args]}"
shift 2

token="$(curl -s -X POST "${url}/api/catalog/v1/oauth/tokens" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -d "grant_type=client_credentials&client_id=${cid}&client_secret=${csecret}&scope=PRINCIPAL_ROLE:ALL" \
  | jq -r '.access_token')"

if [ -z "${token}" ] || [ "${token}" = "null" ]; then
  echo "failed to obtain token from ${url} (is Polaris up? run hack/local-dev/up.sh)" >&2
  exit 1
fi

curl -s -X "${method}" "${url}${endpoint}" \
  -H "Authorization: Bearer ${token}" \
  -H 'Accept: application/json' \
  -H 'Content-Type: application/json' \
  "$@" | jq .
