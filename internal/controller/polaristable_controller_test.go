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

package controller

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisTableReconciler against the real envtest API
// server, mirroring TestPolarisTableReconcile_CreatesWhenMissing in
// data_plane_unit_test.go (same fake-Polaris routes/body shape) but through a
// live API server + a real parent chain instead of the fake client.
var _ = Describe("PolarisTable Controller", func() {
	const testNamespace = "polaristable-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	// seedParents creates a Ready Connection -> Catalog -> Namespace chain
	// (named from the given suffix, so different Contexts in this file don't
	// collide on the same objects) and returns it. envtest's real API server
	// ignores .Status on Create, so each parent's Ready condition (already
	// set by the make* factories) has to be persisted explicitly via
	// Status().Update() — and re-set right before that call, since Create()
	// overwrites the local struct with the server's response, which zeroes
	// .Status (these types all have a status subresource).
	seedParents := func(suffix string) (connName, catName, nsName string) {
		connName, catName, nsName = "prod"+suffix, "lakehouse"+suffix, "analytics"+suffix

		conn := makeConnection(connName, testNamespace)
		Expect(k8sClient.Create(ctx, makeCredentialsSecret(connName, testNamespace))).To(Succeed())
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		conn.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

		cat := makeCatalog(catName, testNamespace, connName)
		Expect(k8sClient.Create(ctx, cat)).To(Succeed())
		cat.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, cat)).To(Succeed())

		ns := makeNamespace(nsName, testNamespace, catName, "")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		ns.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, ns)).To(Succeed())
		return connName, catName, nsName
	}

	Context("When reconciling a table that doesn't exist yet Polaris-side", func() {
		const resourceName = "orders"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("creates the table and reaches Ready=True", func() {
			_, catName, nsName := seedParents("")

			By("creating the PolarisTable CR")
			tbl := &polarisv1alpha1.PolarisTable{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisTableSpec{
					NamespaceRef: polarisv1alpha1.NamespaceRef{Name: nsName},
					Schema: polarisv1alpha1.IcebergSchema{
						Fields: []polarisv1alpha1.IcebergField{{ID: 1, Name: "id", Type: "long", Required: true}},
					},
					WriteFormat: polarisv1alpha1.WriteFormatParquet,
				},
			}
			Expect(k8sClient.Create(ctx, tbl)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			base := "/api/catalog/v1/" + catName + "/namespaces/" + nsName + "/tables"
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", base+"/orders", http.StatusNotFound, nil)
			fp.reply("POST", base, http.StatusOK, map[string]any{
				"metadata-location": "s3://bucket/orders/metadata.json",
				"metadata": map[string]any{
					"format-version": 2,
					"table-uuid":     "uuid-orders",
					"location":       "s3://bucket/orders",
				},
			})

			r := &PolarisTableReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			for range 5 {
				res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())
				if !res.Requeue && res.RequeueAfter == 0 { //nolint:staticcheck // SA1019 on Result.Requeue is intentional
					break
				}
			}

			By("checking status conditions on the real object")
			got := &polarisv1alpha1.PolarisTable{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(got.Status.Location).To(Equal("s3://bucket/orders"))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("When deleting a table", func() {
		const resourceName = "to-delete"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("drops it Polaris-side and removes the finalizer", func() {
			_, catName, nsName := seedParents("-del")

			tbl := &polarisv1alpha1.PolarisTable{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisTableSpec{
					NamespaceRef: polarisv1alpha1.NamespaceRef{Name: nsName},
					Schema: polarisv1alpha1.IcebergSchema{
						Fields: []polarisv1alpha1.IcebergField{{ID: 1, Name: "id", Type: "long", Required: true}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, tbl)).To(Succeed())

			base := "/api/catalog/v1/" + catName + "/namespaces/" + nsName + "/tables"
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", base+"/to-delete", http.StatusNotFound, nil)
			fp.reply("POST", base, http.StatusOK, map[string]any{
				"metadata-location": "s3://bucket/to-delete/metadata.json",
				"metadata":          map[string]any{"format-version": 2, "table-uuid": "uuid", "location": "s3://bucket/to-delete"},
			})
			fp.reply("DELETE", base+"/to-delete", http.StatusOK, nil)

			r := &PolarisTableReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			for range 5 {
				res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
				Expect(err).NotTo(HaveOccurred())
				if !res.Requeue && res.RequeueAfter == 0 { //nolint:staticcheck
					break
				}
			}

			By("deleting the CR and reconciling the delete")
			Expect(k8sClient.Delete(ctx, tbl)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisTable{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
