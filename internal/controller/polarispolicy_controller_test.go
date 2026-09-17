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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisPolicyReconciler against the real envtest API
// server, mirroring TestPolarisPolicyReconcile_CreatesWhenMissing in
// data_plane_unit_test.go (same fake-Polaris routes/body shape) but through a
// live API server + a real parent chain instead of the fake client. Note the
// policy API lives under a distinct "/api/catalog/polaris/v1" prefix, unlike
// tables/views.
var _ = Describe("PolarisPolicy Controller", func() {
	const testNamespace = "polarispolicy-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	// seedParents creates a Ready Connection -> Catalog -> Namespace chain
	// (named from the given suffix, so different Contexts in this file don't
	// collide on the same objects). Each Status().Update() is preceded by
	// re-setting .Status.Conditions because Create() overwrites the local
	// struct with the server's response, which zeroes .Status (these types
	// all have a status subresource).
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

	Context("When reconciling a policy that doesn't exist yet Polaris-side", func() {
		const resourceName = "compact"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("creates the policy and reaches Ready=True", func() {
			_, catName, nsName := seedParents("")

			By("creating the PolarisPolicy CR")
			pol := &polarisv1alpha1.PolarisPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisPolicySpec{
					NamespaceRef: polarisv1alpha1.NamespaceRef{Name: nsName},
					Type:         "system.data-compaction",
					Content:      apiextensionsv1.JSON{Raw: []byte(`{"target_file_size_mb":256}`)},
				},
			}
			Expect(k8sClient.Create(ctx, pol)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			base := "/api/catalog/polaris/v1/" + catName + "/namespaces/" + nsName + "/policies"
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", base+"/compact", http.StatusNotFound, nil)
			fp.route("POST", base, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})

			r := &PolarisPolicyReconciler{
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
			got := &polarisv1alpha1.PolarisPolicy{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("When deleting a policy", func() {
		const resourceName = "to-delete"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("drops it Polaris-side and removes the finalizer", func() {
			_, catName, nsName := seedParents("-del")

			pol := &polarisv1alpha1.PolarisPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisPolicySpec{
					NamespaceRef: polarisv1alpha1.NamespaceRef{Name: nsName},
					Type:         "system.data-compaction",
					Content:      apiextensionsv1.JSON{Raw: []byte(`{"target_file_size_mb":256}`)},
				},
			}
			Expect(k8sClient.Create(ctx, pol)).To(Succeed())

			base := "/api/catalog/polaris/v1/" + catName + "/namespaces/" + nsName + "/policies"
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", base+"/to-delete", http.StatusNotFound, nil)
			fp.route("POST", base, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			fp.reply("DELETE", base+"/to-delete", http.StatusOK, nil)

			r := &PolarisPolicyReconciler{
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
			Expect(k8sClient.Delete(ctx, pol)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisPolicy{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)
		})
	})
})
