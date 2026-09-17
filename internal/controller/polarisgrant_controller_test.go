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

// These specs use the simplest discriminated-union target (catalog-level —
// see buildGrantBody in polarisgrant_controller.go, which returns early for
// GrantTargetCatalog without needing a namespace/table/view ref). The
// namespace-target path is already exercised in
// TestPolarisGrantReconcile_NamespaceTarget (bindings_grant_unit_test.go);
// this suite's job is proving the real API server integration, not
// re-covering every target-type branch.
var _ = Describe("PolarisGrant Controller", func() {
	const testNamespace = "polarisgrant-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	// makeReadyParents creates a Ready Connection, Catalog and CatalogRole in
	// the real API server.
	makeReadyParents := func(suffix string) {
		conn := makeConnection("prod-"+suffix, testNamespace)
		secret := makeCredentialsSecret("prod-"+suffix, testNamespace)
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		conn.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())

		cat := makeCatalog("lakehouse-"+suffix, testNamespace, "prod-"+suffix)
		Expect(k8sClient.Create(ctx, cat)).To(Succeed())
		cat.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, cat)).To(Succeed())

		cr := &polarisv1alpha1.PolarisCatalogRole{
			ObjectMeta: metav1.ObjectMeta{Name: "rw-" + suffix, Namespace: testNamespace},
			Spec: polarisv1alpha1.PolarisCatalogRoleSpec{
				CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse-" + suffix},
				Name:       "rw-" + suffix,
			},
		}
		Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		cr.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, cr)).To(Succeed())
	}

	Context("When reconciling a catalog-target grant", func() {
		const suffix = "healthy"
		resourceName := "rw-catalog-" + suffix
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("reaches Ready=True and PUTs the grant", func() {
			makeReadyParents(suffix)

			grant := &polarisv1alpha1.PolarisGrant{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisGrantSpec{
					CatalogRoleRef: polarisv1alpha1.CatalogRoleRef{Name: "rw-" + suffix},
					Privilege:      polarisv1alpha1.Privilege("CATALOG_MANAGE_CONTENT"),
					Target:         polarisv1alpha1.GrantTarget{Type: polarisv1alpha1.GrantTargetCatalog},
				},
			}
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("PUT", "/api/management/v1/catalogs/lakehouse-"+suffix+"/catalog-roles/rw-"+suffix+"/grants",
				http.StatusNoContent, nil)

			r := &PolarisGrantReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			got := &polarisv1alpha1.PolarisGrant{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
		})
	})

	Context("When deleting a grant", func() {
		const suffix = "delete"
		resourceName := "rw-catalog-" + suffix
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("revokes the grant and removes the finalizer", func() {
			makeReadyParents(suffix)

			grant := &polarisv1alpha1.PolarisGrant{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisGrantSpec{
					CatalogRoleRef: polarisv1alpha1.CatalogRoleRef{Name: "rw-" + suffix},
					Privilege:      polarisv1alpha1.Privilege("CATALOG_MANAGE_CONTENT"),
					Target:         polarisv1alpha1.GrantTarget{Type: polarisv1alpha1.GrantTargetCatalog},
				},
			}
			Expect(k8sClient.Create(ctx, grant)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("PUT", "/api/management/v1/catalogs/lakehouse-"+suffix+"/catalog-roles/rw-"+suffix+"/grants",
				http.StatusNoContent, nil)
			fp.reply("POST", "/api/management/v1/catalogs/lakehouse-"+suffix+"/catalog-roles/rw-"+suffix+"/grants",
				http.StatusNoContent, nil)

			r := &PolarisGrantReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Delete(ctx, grant)).To(Succeed())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			got := &polarisv1alpha1.PolarisGrant{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
