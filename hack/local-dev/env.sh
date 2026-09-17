# Source this to point your shell at the harness:
#
#   source hack/local-dev/env.sh
#
# It sets KUBECONFIG to the isolated kind cluster (so `kubectl` only ever sees
# the local cluster, never prod/staging) and exports the Polaris URLs.
_localdev_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"

export KUBECONFIG="${_localdev_dir}/.kubeconfig"
export POLARIS_URL="http://localhost:8181"   # Iceberg REST + management API
export POLARIS_MGMT_URL="http://localhost:8182" # health + metrics
export POLARIS_CLIENT_ID="root"
export POLARIS_CLIENT_SECRET="s3cr3t"

if kubectl config current-context >/dev/null 2>&1; then
  echo "KUBECONFIG -> ${KUBECONFIG} (context: $(kubectl config current-context))"
else
  echo "KUBECONFIG -> ${KUBECONFIG} (cluster not up — run hack/local-dev/up.sh)"
fi
echo "POLARIS_URL -> ${POLARIS_URL}   POLARIS_MGMT_URL -> ${POLARIS_MGMT_URL}"
