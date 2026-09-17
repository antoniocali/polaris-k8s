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

// These specs drive PolarisCatalogRoleReconciler against the real envtest API
// server, mirroring TestPolarisCatalogRoleReconcile_CreatesWhenMissing from
// iam_unit_test.go, but through a live API server + a real Ready parent
// PolarisCatalog instead of the fake client.
var _ = Describe("PolarisCatalogRole Controller", func() {
	const testNamespace = "polariscatalogrole-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	reconcileCatalogRoleUntilSettled := func(r *PolarisCatalogRoleReconciler, nn types.NamespacedName) {
		for range 5 {
			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			if !res.Requeue && res.RequeueAfter == 0 { //nolint:staticcheck // SA1019 on Result.Requeue is intentional
				return
			}
		}
		Fail("catalog role reconcile did not settle after 5 iterations")
	}

	Context("When reconciling a catalog role whose parent catalog is Ready", func() {
		const connName = "prod"
		const catName = "lakehouse"
		const roleName = "rw"
		nn := types.NamespacedName{Name: roleName, Namespace: testNamespace}

		It("creates the catalog role Polaris-side and reaches Ready=True", func() {
			By("standing up a Ready connection and a Ready catalog")
			secret := makeCredentialsSecret(connName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(connName, testNamespace)
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())
			conn.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

			cat := makeCatalog(catName, testNamespace, connName)
			Expect(k8sClient.Create(ctx, cat)).To(Succeed())
			cat.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, cat)).To(Succeed())

			By("creating the PolarisCatalogRole CR")
			cr := &polarisv1alpha1.PolarisCatalogRole{
				ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: testNamespace},
				Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: catName}},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/catalogs/"+catName+"/catalog-roles/"+roleName, http.StatusNotFound, nil)
			fp.route("POST", "/api/management/v1/catalogs/"+catName+"/catalog-roles", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			r := &PolarisCatalogRoleReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileCatalogRoleUntilSettled(r, nn)

			By("checking status on the real object")
			got := &polarisv1alpha1.PolarisCatalogRole{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(controllerutil.ContainsFinalizer(got, PolarisFinalizer)).To(BeTrue())
		})
	})

	Context("When deleting a catalog role", func() {
		const connName = "prod-del"
		const catName = "lakehouse-del"
		const roleName = "to-delete"
		nn := types.NamespacedName{Name: roleName, Namespace: testNamespace}

		It("deletes the catalog role Polaris-side and removes the finalizer", func() {
			By("standing up a Ready connection, Ready catalog, and a synced catalog role")
			secret := makeCredentialsSecret(connName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(connName, testNamespace)
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())
			conn.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

			cat := makeCatalog(catName, testNamespace, connName)
			Expect(k8sClient.Create(ctx, cat)).To(Succeed())
			cat.Status.Conditions = readyConditions()
			Expect(k8sClient.Status().Update(ctx, cat)).To(Succeed())

			cr := &polarisv1alpha1.PolarisCatalogRole{
				ObjectMeta: metav1.ObjectMeta{Name: roleName, Namespace: testNamespace},
				Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: catName}},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/catalogs/"+catName+"/catalog-roles/"+roleName, http.StatusNotFound, nil)
			fp.route("POST", "/api/management/v1/catalogs/"+catName+"/catalog-roles", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			var deleteCalled bool
			fp.route("DELETE", "/api/management/v1/catalogs/"+catName+"/catalog-roles/"+roleName, func(w http.ResponseWriter, r *http.Request) {
				deleteCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			r := &PolarisCatalogRoleReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileCatalogRoleUntilSettled(r, nn)

			By("deleting the CR and reconciling the delete")
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(deleteCalled).To(BeTrue(), "DELETE on the catalog role was not called during finalize")

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisCatalogRole{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
