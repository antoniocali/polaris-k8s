//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/antoniocali/polaris-k8s/test/utils"
)

// This file drives a real Apache Polaris server (same image/config as
// hack/local-dev/docker-compose.yaml) as a sibling Docker container next to
// the Kind cluster, then applies the same object graph the local-dev harness
// uses (hack/local-dev/k8s/{connection,sample-crs}.yaml) against the
// operator actually deployed in-cluster, plus a PolarisTable and
// PolarisView (not in the local-dev sample). This is the one place the
// whole stack — real CRDs, real controller, real Polaris — is proven
// together; per-kind reconciler behavior is covered by the envtest suites
// in internal/controller.
//
// PolarisPolicy is intentionally left out: its `content` field is validated
// by Polaris against a policy-type-specific JSON schema we don't have solid
// enough documentation of to assert against a real server without risking a
// flaky false-negative; it's still covered by its envtest suite (fake
// Polaris backend, so schema validity there doesn't matter).

const (
	polarisContainerName = "polaris-e2e"
	polarisDevNamespace  = "polaris-dev"
)

// startPolarisForE2E runs a real Apache Polaris container on the same Docker
// network as the Kind nodes ("kind"), so pods inside the cluster can reach
// it. It returns the container's IP address on that network — used instead
// of the container name because pod -> sibling-container DNS resolution
// through CoreDNS's upstream forwarding isn't reliably guaranteed, while
// routing to the sibling's IP (both are on the same Docker bridge) is.
func startPolarisForE2E() (string, error) {
	By("starting a real Apache Polaris container for the e2e scenario")
	cmd := exec.Command("docker", "run", "-d", "--rm",
		"--name", polarisContainerName,
		"--network", "kind",
		"-e", "POLARIS_BOOTSTRAP_CREDENTIALS=POLARIS,root,s3cr3t",
		"-e", "polaris.realm-context.realms=POLARIS",
		"-e", "quarkus.otel.sdk.disabled=true",
		"-e", `polaris.features."SUPPORTED_CATALOG_STORAGE_TYPES"=["S3","GCS","AZURE","FILE"]`,
		"-e", `polaris.features."ALLOW_INSECURE_STORAGE_TYPES"=true`,
		"-e", "polaris.readiness.ignore-severe-issues=true",
		"apache/polaris:latest",
	)
	if _, err := utils.Run(cmd); err != nil {
		return "", fmt.Errorf("start polaris container: %w", err)
	}

	By("waiting for Polaris to report healthy")
	Eventually(func(g Gomega) {
		_, err := utils.Run(exec.Command("docker", "exec", polarisContainerName,
			"curl", "--fail", "--silent", "http://localhost:8182/q/health"))
		g.Expect(err).NotTo(HaveOccurred())
	}, 90*time.Second, 2*time.Second).Should(Succeed())

	ipOut, err := utils.Run(exec.Command("docker", "inspect", "-f",
		"{{.NetworkSettings.Networks.kind.IPAddress}}", polarisContainerName))
	if err != nil {
		return "", fmt.Errorf("inspect polaris container IP: %w", err)
	}
	ip := strings.TrimSpace(ipOut)
	if ip == "" {
		return "", fmt.Errorf("polaris container has no IP on the kind network")
	}
	return ip, nil
}

// stopPolarisForE2E tears down the Polaris container. Errors are logged,
// not failed — this runs from AfterAll/cleanup paths.
func stopPolarisForE2E() {
	By("stopping the Polaris container")
	if _, err := utils.Run(exec.Command("docker", "rm", "-f", polarisContainerName)); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "warning: failed to remove polaris container: %v\n", err)
	}
}

// applyPolarisObjectGraph re-uses the local-dev harness's own manifests
// (hack/local-dev/k8s/connection.yaml + sample-crs.yaml — a real, already
// end-to-end-exercised object graph: Catalog, Namespace, Principal,
// PrincipalRole, CatalogRole, both RoleBinding kinds, and a Grant) rather
// than hand-duplicating a second copy of the same CRs, swapping only the
// serverUrl (which points at localhost for the local-dev harness, where the
// operator runs on the host — here the operator runs in-cluster, so it
// needs the sibling container's IP instead), and appends a PolarisTable +
// PolarisView (not present in the local-dev sample) to also exercise the
// data-plane kinds.
func applyPolarisObjectGraph(polarisIP string) (string, error) {
	projectDir, err := utils.GetProjectDir()
	if err != nil {
		return "", err
	}

	var combined strings.Builder
	for _, name := range []string{"connection.yaml", "sample-crs.yaml"} {
		content, err := os.ReadFile(filepath.Join(projectDir, "hack", "local-dev", "k8s", name))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", name, err)
		}
		combined.WriteString(strings.ReplaceAll(
			string(content), "http://localhost:8181", fmt.Sprintf("http://%s:8181", polarisIP)))
		combined.WriteString("\n---\n")
	}

	combined.WriteString(fmt.Sprintf(`
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisTable
metadata:
  name: orders
  namespace: %[1]s
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
        required: true
      - id: 2
        name: amount
        type: "decimal(10,2)"
---
apiVersion: polaris.k8s.calific.io/v1alpha1
kind: PolarisView
metadata:
  name: orders-summary
  namespace: %[1]s
spec:
  namespaceRef:
    name: analytics
  schema:
    fields:
      - id: 1
        name: id
        type: long
  sql: "SELECT id FROM analytics.orders"
`, polarisDevNamespace))

	manifestPath := filepath.Join(os.TempDir(), "polaris-k8s-e2e-graph.yaml")
	if err := os.WriteFile(manifestPath, []byte(combined.String()), 0o600); err != nil {
		return "", fmt.Errorf("write combined manifest: %w", err)
	}
	return manifestPath, nil
}

// waitForReady polls a CR's Ready condition until it's True.
func waitForReady(kind, name, ns string) {
	Eventually(func(g Gomega) {
		out, err := utils.Run(exec.Command("kubectl", "get", kind, name, "-n", ns,
			"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(strings.TrimSpace(out)).To(Equal("True"), "%s/%s in %s not Ready yet", kind, name, ns)
	}, 2*time.Minute, 2*time.Second).Should(Succeed())
}
