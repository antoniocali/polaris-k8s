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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// These specs drive PolarisConnectionReconciler against the real envtest API
// server (CRD schema, status subresource, finalizer semantics all real),
// while faking only the Polaris HTTP backend — the same boundary the
// TestPolarisConnectionReconcile_* unit tests in
// polarisconnection_unit_test.go use via the fake client, just now through a
// live API server instead.
var _ = Describe("PolarisConnection Controller", func() {
	const testNamespace = "polarisconnection-test"

	BeforeEach(func() {
		Expect(ensureNamespace(ctx, k8sClient, testNamespace)).To(Succeed())
	})

	Context("When reconciling a healthy connection", func() {
		const resourceName = "prod"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		AfterEach(func() {
			conn := &polarisv1alpha1.PolarisConnection{}
			if err := k8sClient.Get(ctx, nn, conn); err == nil {
				Expect(k8sClient.Delete(ctx, conn)).To(Succeed())
			}
			secret := makeCredentialsSecret(resourceName, testNamespace)
			_ = k8sClient.Delete(ctx, secret)
		})

		It("reaches Ready=True and AuthValid=True", func() {
			By("creating the credentials Secret and the PolarisConnection")
			secret := makeCredentialsSecret(resourceName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(resourceName, testNamespace)
			conn.Status = polarisv1alpha1.PolarisConnectionStatus{} // Create ignores status anyway
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())

			By("reconciling with a fake Polaris backend")
			fp := newFakePolaris(GinkgoT())
			r := &PolarisConnectionReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("checking status conditions on the real object")
			got := &polarisv1alpha1.PolarisConnection{}
			Expect(k8sClient.Get(ctx, nn, got)).To(Succeed())
			Expect(gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(gotCondition(got.Status.Conditions, ConditionAuthValid, metav1.ConditionTrue)).
				To(BeTrue(), dumpConditions(got.Status.Conditions))
			Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		})
	})

	Context("When deleting a connection", func() {
		const resourceName = "to-delete"
		nn := types.NamespacedName{Name: resourceName, Namespace: testNamespace}

		It("is removed immediately (no remote state, no finalizer)", func() {
			By("creating and reconciling the connection")
			secret := makeCredentialsSecret(resourceName, testNamespace)
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			conn := makeConnection(resourceName, testNamespace)
			conn.Status = polarisv1alpha1.PolarisConnectionStatus{}
			Expect(k8sClient.Create(ctx, conn)).To(Succeed())

			fp := newFakePolaris(GinkgoT())
			r := &PolarisConnectionReconciler{
				Client:             k8sClient,
				Scheme:             k8sClient.Scheme(),
				BuildPolarisClient: fakeBuilder(GinkgoT(), fp),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("deleting the connection and reconciling the delete")
			Expect(k8sClient.Delete(ctx, conn)).To(Succeed())
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: nn})
			Expect(err).NotTo(HaveOccurred())

			By("confirming the object is actually gone")
			got := &polarisv1alpha1.PolarisConnection{}
			err = k8sClient.Get(ctx, nn, got)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected NotFound, got: %v", err)

			_ = k8sClient.Delete(context.Background(), secret)
		})
	})
})
