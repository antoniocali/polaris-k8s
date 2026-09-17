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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisNamespaceReconciler against the real envtest API
// server through a full three-level real parent chain (Connection -> Catalog
// -> Namespace), mirroring TestPolarisNamespaceReconcile_CreatesTopLevel and
// TestPolarisNamespaceReconcile_DropOnFinalize from
// polarisnamespace_unit_test.go. nsURLPath and PolarisFinalizer are shared
// package-level helpers from that file.
var _ = Describe("PolarisNamespace Controller", func() {
	const testNamespace = "polarisnamespace-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	reconcileNamespaceUntilSettled := func(r *PolarisNamespaceReconciler, nn types.NamespacedName) {
		for range 5 {
			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			if !res.Requeue && res.RequeueAfter == 0 { //nolint:staticcheck // SA1019 on Result.Requeue is intentional
				return
			}
		}
		Fail("namespace reconcile did not settle after 5 iterations")
	}

	Context("When reconciling a top-level namespace whose parent chain is Ready", func() {
		const connName = "prod"
		const catName = "lakehouse"
		const nsName = "analytics"
		nn := types.NamespacedName{Name: nsName, Namespace: testNamespace}

		It("creates the namespace Polaris-side and reaches Ready=True", func() {
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

			By("creating the PolarisNamespace CR")
			polarisNS := makeNamespace(nsName, testNamespace, catName, "")
			Expect(k8sClient.Create(ctx, polarisNS)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", nsURLPath(catName, []string{nsName}), http.StatusNotFound, nil)
			fp.route("POST", "/api/catalog/v1/"+catName+"/namespaces", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			r := &PolarisNamespaceReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileNamespaceUntilSettled(r, nn)

			By("checking status on the real object")
			got := &polarisv1alpha1.PolarisNamespace{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(reflect.DeepEqual(got.Status.FullPath, []string{nsName})).To(BeTrue())
			Expect(controllerutil.ContainsFinalizer(got, PolarisFinalizer)).To(BeTrue())
		})
	})

	Context("When deleting a namespace", func() {
		const connName = "prod-del"
		const catName = "lakehouse-del"
		const nsName = "to-delete"
		nn := types.NamespacedName{Name: nsName, Namespace: testNamespace}

		It("drops the namespace Polaris-side and removes the finalizer", func() {
			By("standing up a Ready connection, Ready catalog, and a synced namespace")
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

			polarisNS := makeNamespace(nsName, testNamespace, catName, "")
			Expect(k8sClient.Create(ctx, polarisNS)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", nsURLPath(catName, []string{nsName}), http.StatusNotFound, nil)
			fp.route("POST", "/api/catalog/v1/"+catName+"/namespaces", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			var dropCalled bool
			fp.route("DELETE", nsURLPath(catName, []string{nsName}), func(w http.ResponseWriter, r *http.Request) {
				dropCalled = true
				w.WriteHeader(http.StatusNoContent)
			})
			r := &PolarisNamespaceReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			reconcileNamespaceUntilSettled(r, nn)

			By("deleting the CR and reconciling the delete")
			Expect(k8sClient.Delete(ctx, polarisNS)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())
			Expect(dropCalled).To(BeTrue(), "DELETE on the namespace was not called during finalize")

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisNamespace{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
