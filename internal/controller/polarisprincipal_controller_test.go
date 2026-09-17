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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisPrincipalReconciler against the real envtest API
// server, mirroring TestPolarisPrincipalReconcile_CreatesAndWritesSecret in
// iam_unit_test.go (same fake-Polaris routes/body shape) but through a live
// API server + a real parent Connection instead of the fake client — which
// also lets us exercise the real Secret ownerRef/GC-relevant plumbing that
// the fake client can't meaningfully validate.
var _ = Describe("PolarisPrincipal Controller", func() {
	const testNamespace = "polarisprincipal-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	// seedConnection creates a Ready Connection named from the given suffix,
	// so the two Contexts in this file don't collide on the same objects.
	seedConnection := func(suffix string) (connName string) {
		connName = "prod" + suffix
		conn := makeConnection(connName, testNamespace)
		Expect(k8sClient.Create(ctx, makeCredentialsSecret(connName, testNamespace))).To(Succeed())
		Expect(k8sClient.Create(ctx, conn)).To(Succeed())
		// Create() overwrites conn with the server's response, which zeroes
		// .Status (it has a status subresource) — re-set it before persisting.
		conn.Status.Conditions = readyConditions()
		Expect(k8sClient.Status().Update(ctx, conn)).To(Succeed())
		return connName
	}

	Context("When reconciling a principal that doesn't exist yet Polaris-side", func() {
		const resourceName = "airflow"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("creates the principal, reaches Ready=True and writes the credentials Secret", func() {
			connName := seedConnection("")

			By("creating the PolarisPrincipal CR")
			pp := &polarisv1alpha1.PolarisPrincipal{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisPrincipalSpec{
					ConnectionRef:        polarisv1alpha1.ConnectionRef{Name: connName},
					CredentialsSecretRef: polarisv1alpha1.GeneratedCredentialsSecretRef{Name: "airflow-polaris"},
				},
			}
			Expect(k8sClient.Create(ctx, pp)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/principals/airflow", http.StatusNotFound, nil)
			fp.reply("POST", "/api/management/v1/principals", http.StatusCreated, map[string]any{
				"principal": map[string]any{"name": "airflow", "clientId": "abc123"},
				"credentials": map[string]any{
					"clientId":     "abc123",
					"clientSecret": "supersecret",
				},
			})

			r := &PolarisPrincipalReconciler{
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
			got := &polarisv1alpha1.PolarisPrincipal{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(gotCondition(got.Status.Conditions, ConditionCredentialsWritten, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))

			By("checking the generated credentials Secret, real ownerRef included")
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "airflow-polaris", Namespace: testNamespace}, secret)).To(Succeed())
			Expect(string(secret.Data["clientId"])).To(Equal("abc123"))
			Expect(string(secret.Data["clientSecret"])).To(Equal("supersecret"))
			Expect(secret.OwnerReferences).To(HaveLen(1))
			Expect(secret.OwnerReferences[0].Name).To(Equal(resourceName))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
			_ = k8sClient.Delete(ctx, secret)
		})
	})

	Context("When deleting a principal", func() {
		const resourceName = "to-delete"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("deletes it Polaris-side and removes the finalizer", func() {
			connName := seedConnection("-del")

			pp := &polarisv1alpha1.PolarisPrincipal{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: testNamespace},
				Spec: polarisv1alpha1.PolarisPrincipalSpec{
					ConnectionRef:        polarisv1alpha1.ConnectionRef{Name: connName},
					CredentialsSecretRef: polarisv1alpha1.GeneratedCredentialsSecretRef{Name: "to-delete-polaris"},
				},
			}
			Expect(k8sClient.Create(ctx, pp)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			fp.reply("GET", "/api/management/v1/principals/to-delete", http.StatusNotFound, nil)
			fp.reply("POST", "/api/management/v1/principals", http.StatusCreated, map[string]any{
				"principal":   map[string]any{"name": "to-delete", "clientId": "id-1"},
				"credentials": map[string]any{"clientId": "id-1", "clientSecret": "secret-1"},
			})
			fp.reply("DELETE", "/api/management/v1/principals/to-delete", http.StatusOK, nil)

			r := &PolarisPrincipalReconciler{
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
			Expect(k8sClient.Delete(ctx, pp)).To(Succeed())
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisPrincipal{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)

			_ = k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "to-delete-polaris", Namespace: testNamespace}})
		})
	})
})
