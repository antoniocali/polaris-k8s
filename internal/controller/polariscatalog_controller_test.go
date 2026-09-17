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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisCatalogReconciler against the real envtest API
// server, mirroring TestPolarisCatalogReconcile_CreatesWhenMissing and
// TestPolarisCatalogReconcile_DeletesOnFinalize from
// polariscatalog_unit_test.go, but through a live API server + a real parent
// PolarisConnection instead of the fake client.
var _ = Describe("PolarisCatalog Controller", func() {
	const testNamespace = "polariscatalog-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	// reconcileCatalogUntilSettled drives Reconcile until it stops asking for
	// a requeue — the first call only adds the finalizer.
	reconcileCatalogUntilSettled := func(r *PolarisCatalogReconciler, nn types.NamespacedName) {
		for range 5 {
			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			if !res.Requeue && res.RequeueAfter == 0 { //nolint:staticcheck // SA1019 on Result.Requeue is intentional
				return
			}
		}
		Fail("catalog reconcile did not settle after 5 iterations")
	}

	Context("When reconciling a catalog whose parent connection is Ready", func() {
		const connName = "prod"
		const catName = "lakehouse"
		nn := types.NamespacedName{Name: catName, Namespace: testNamespace}

		It("creates the catalog Polaris-side and reaches Ready=True", func() {
			By("standing up a Ready parent connection")
			secret := makeCredentialsSecret(connName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(connName, testNamespace)
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())
			conn.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

			By("creating the PolarisCatalog CR")
			cat := makeCatalog(catName, testNamespace, connName)
			Expect(k8sClient.Create(ctx, cat)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/catalogs/"+catName, http.StatusNotFound, map[string]string{"error": "not found"})
			fp.route("POST", "/api/management/v1/catalogs", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			r := &PolarisCatalogReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileCatalogUntilSettled(r, nn)

			By("checking status on the real object")
			got := &polarisv1alpha1.PolarisCatalog{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(got.Status.PolarisCatalogID).To(Equal(catName))
			Expect(controllerutil.ContainsFinalizer(got, PolarisFinalizer)).To(BeTrue())
		})
	})

	Context("When deleting a catalog", func() {
		const connName = "prod-del"
		const catName = "to-delete"
		nn := types.NamespacedName{Name: catName, Namespace: testNamespace}

		It("deletes the catalog Polaris-side and removes the finalizer", func() {
			By("standing up a Ready parent connection and a synced catalog")
			secret := makeCredentialsSecret(connName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(connName, testNamespace)
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())
			conn.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

			cat := makeCatalog(catName, testNamespace, connName)
			Expect(k8sClient.Create(ctx, cat)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/catalogs/"+catName, http.StatusNotFound, map[string]string{"error": "not found"})
			fp.route("POST", "/api/management/v1/catalogs", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			var deleteCalled bool
			fp.route("DELETE", "/api/management/v1/catalogs/"+catName, func(w http.ResponseWriter, r *http.Request) {
				deleteCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			r := &PolarisCatalogReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileCatalogUntilSettled(r, nn)

			By("deleting the CR and reconciling the delete")
			Expect(k8sClient.Delete(ctx, cat)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteCalled).To(BeTrue(), "DELETE /catalogs/"+catName+" was not called during finalize")

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisCatalog{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
