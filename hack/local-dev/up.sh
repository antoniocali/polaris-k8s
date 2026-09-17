#!/usr/bin/env bash
# Bring up the local-dev harness: a kind cluster + Tilt.
#
# The cluster is created against your DEFAULT kubeconfig, so kind registers the
# `kind-polaris-dev` context in ~/.kube/config — your everyday kubectl reaches
# it with `kubectl --context kind-polaris-dev`. Tilt and the host-run operator
# are then pinned to an isolated kubeconfig so they only ever act on this
# cluster, never on whatever context (prod/staging) you happen to be using.
set -euo pipefail

CLUSTER=polaris-dev
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Create the cluster WITHOUT the isolated KUBECONFIG set, so kind merges the
# context into your default ~/.kube/config (standard kind behavior).
if ! kind get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
  echo "==> creating kind cluster '${CLUSTER}'"
  kind create cluster --name "${CLUSTER}"
fi

# Isolated kubeconfig for Tilt + the operator (this session only).
export KUBECONFIG="${DIR}/.kubeconfig"
kind export kubeconfig --name "${CLUSTER}" --kubeconfig "${KUBECONFIG}"

echo "==> Tilt/operator KUBECONFIG=${KUBECONFIG} (context: $(kubectl config current-context))"
echo "==> from any other shell: kubectl --context kind-${CLUSTER} -n polaris-dev get polaris"
echo "==> starting Tilt — UI at http://localhost:10350"
cd "${DIR}"
exec tilt up
