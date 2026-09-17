#!/usr/bin/env bash
# Tear down everything the harness created.
set -euo pipefail

CLUSTER=polaris-dev
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export KUBECONFIG="${DIR}/.kubeconfig"

echo "==> tilt down"
( cd "${DIR}" && tilt down ) || true

echo "==> docker compose down"
docker compose -f "${DIR}/docker-compose.yaml" down -v || true

echo "==> deleting kind cluster '${CLUSTER}'"
kind delete cluster --name "${CLUSTER}" || true

rm -f "${KUBECONFIG}"
echo "==> done"
