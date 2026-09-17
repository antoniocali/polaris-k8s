/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// reconcileUntilSynced drives Reconcile until it stops asking for a requeue.
// We still check Result.Requeue (deprecated in controller-runtime) because
// it's the standard idiom for the "added finalizer, please run again"
// handshake — until reconcilers migrate to a different signalling scheme.
//
//nolint:unparam,staticcheck // ns generic for future tests; SA1019 on Result.Requeue is intentional
func reconcileUntilSynced(t *testing.T, reconcile func(context.Context, ctrl.Request) (ctrl.Result, error), name, ns string) {
	t.Helper()
	ctx := context.Background()
	for i := range 5 {
		res, err := reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		if err != nil {
			t.Fatalf("Reconcile[%d]: %v", i, err)
		}
		if !res.Requeue && res.RequeueAfter == 0 {
			return
		}
	}
	t.Fatalf("Reconcile did not settle")
}

func TestPolarisPrincipalRoleReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/management/v1/principal-roles/writer", http.StatusNotFound, nil)
	var created atomic.Bool
	fp.route("POST", "/api/management/v1/principal-roles", func(w http.ResponseWriter, r *http.Request) {
		created.Store(true)
		w.WriteHeader(http.StatusCreated)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	pr := &polarisv1alpha1.PolarisPrincipalRole{
		ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisPrincipalRoleSpec{ConnectionRef: polarisv1alpha1.ConnectionRef{Name: "prod"}},
	}
	c := newFakeClient(t, conn, secret, pr)

	r := &PolarisPrincipalRoleReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "writer", "data-platform")

	if !created.Load() {
		t.Fatal("POST /principal-roles not called")
	}
	got := &polarisv1alpha1.PolarisPrincipalRole{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "writer", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisCatalogRoleReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/management/v1/catalogs/lakehouse/catalog-roles/rw", http.StatusNotFound, nil)
	var created atomic.Bool
	fp.route("POST", "/api/management/v1/catalogs/lakehouse/catalog-roles", func(w http.ResponseWriter, r *http.Request) {
		created.Store(true)
		w.WriteHeader(http.StatusCreated)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}},
	}
	c := newFakeClient(t, conn, secret, cat, cr)

	r := &PolarisCatalogRoleReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "rw", "data-platform")

	if !created.Load() {
		t.Fatal("POST /catalog-roles not called")
	}
	got := &polarisv1alpha1.PolarisCatalogRole{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "rw", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
}

// TestPolarisCatalogRoleReconcile_WaitsForUnreadyCatalog covers the
// dependency gate: when the parent catalog exists but hasn't reconciled yet
// (no Ready condition), the catalog-role must requeue quietly — no error, no
// Polaris call — and surface Ready=False/DependencyNotReady instead of 404ing.
func TestPolarisCatalogRoleReconcile_WaitsForUnreadyCatalog(t *testing.T) {
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cat.Status.Conditions = nil // parent not reconciled yet
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}},
	}
	c := newFakeClient(t, cat, cr)

	// No BuildPolarisClient on purpose: the gate must return before any client
	// is built or any Polaris call is attempted. A nil builder would panic if
	// reached.
	r := &PolarisCatalogRoleReconciler{Client: c, Scheme: c.Scheme()}
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "rw", Namespace: "data-platform"},
	})
	if err != nil {
		t.Fatalf("Reconcile should wait quietly (nil error) when the parent isn't ready, got: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected a RequeueAfter dependency wait, got %+v", res)
	}

	got := &polarisv1alpha1.PolarisCatalogRole{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "rw", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	var ready *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == ConditionReady {
			ready = &got.Status.Conditions[i]
		}
	}
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != ReasonDependencyNotReady {
		t.Errorf("want Ready=False/%s, got:%s", ReasonDependencyNotReady, dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisPrincipalReconcile_CreatesAndWritesSecret(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/management/v1/principals/airflow", http.StatusNotFound, nil)
	fp.reply("POST", "/api/management/v1/principals", http.StatusCreated, map[string]any{
		"principal": map[string]any{"name": "airflow", "clientId": "abc123"},
		"credentials": map[string]any{
			"clientId":     "abc123",
			"clientSecret": "supersecret",
		},
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	pp := &polarisv1alpha1.PolarisPrincipal{
		ObjectMeta: metav1.ObjectMeta{Name: "airflow", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisPrincipalSpec{
			ConnectionRef:        polarisv1alpha1.ConnectionRef{Name: "prod"},
			CredentialsSecretRef: polarisv1alpha1.GeneratedCredentialsSecretRef{Name: "airflow-polaris"},
		},
	}
	c := newFakeClient(t, conn, secret, pp)

	r := &PolarisPrincipalReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "airflow", "data-platform")

	got := &polarisv1alpha1.PolarisPrincipal{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "airflow", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch principal: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
	if !gotCondition(got.Status.Conditions, ConditionCredentialsWritten, metav1.ConditionTrue) {
		t.Errorf("CredentialsWritten=True not set:%s", dumpConditions(got.Status.Conditions))
	}

	credsOut := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "airflow-polaris", Namespace: "data-platform"}, credsOut); err != nil {
		t.Fatalf("re-fetch credentials Secret: %v", err)
	}
	if string(credsOut.Data["clientId"]) != "abc123" || string(credsOut.Data["clientSecret"]) != "supersecret" {
		t.Errorf("Secret data wrong: %v", credsOut.Data)
	}
	// Owned by the principal so K8s GC cleans it up if the CR is deleted.
	if len(credsOut.OwnerReferences) != 1 || credsOut.OwnerReferences[0].Name != "airflow" {
		t.Errorf("expected ownerRef to PolarisPrincipal/airflow, got %+v", credsOut.OwnerReferences)
	}
}

// TestPolarisPrincipalReconcile_RotationOnCreateDoesNotDoubleIssue guards the
// fix for the create-path double-write: a principal created with
// credentialRotationRequired=true gets its rotation honoured by the create
// call itself (CreatePrincipal forwards the flag and returns initial creds).
// The reconciler must NOT then call RotateCredentials again — doing so would
// invalidate the freshly issued credential and write the Secret twice.
func TestPolarisPrincipalReconcile_RotationOnCreateDoesNotDoubleIssue(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/management/v1/principals/airflow", http.StatusNotFound, nil)
	fp.reply("POST", "/api/management/v1/principals", http.StatusCreated, map[string]any{
		"principal": map[string]any{"name": "airflow", "clientId": "abc123"},
		"credentials": map[string]any{
			"clientId":     "abc123",
			"clientSecret": "initial-secret",
		},
	})

	var rotateCalled atomic.Bool
	fp.route("POST", "/api/management/v1/principals/airflow/rotate", func(w http.ResponseWriter, r *http.Request) {
		rotateCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"principal":{"name":"airflow","clientId":"abc123"},"credentials":{"clientId":"abc123","clientSecret":"rotated-secret"}}`))
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	pp := &polarisv1alpha1.PolarisPrincipal{
		ObjectMeta: metav1.ObjectMeta{Name: "airflow", Namespace: "data-platform", Generation: 1},
		Spec: polarisv1alpha1.PolarisPrincipalSpec{
			ConnectionRef:              polarisv1alpha1.ConnectionRef{Name: "prod"},
			CredentialsSecretRef:       polarisv1alpha1.GeneratedCredentialsSecretRef{Name: "airflow-polaris"},
			CredentialRotationRequired: true,
		},
	}
	c := newFakeClient(t, conn, secret, pp)

	r := &PolarisPrincipalReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "airflow", "data-platform")

	if rotateCalled.Load() {
		t.Error("RotateCredentials was called on the create path — credential issued twice")
	}

	credsOut := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "airflow-polaris", Namespace: "data-platform"}, credsOut); err != nil {
		t.Fatalf("re-fetch credentials Secret: %v", err)
	}
	if string(credsOut.Data["clientSecret"]) != "initial-secret" {
		t.Errorf("Secret holds %q, want the initial create credential", credsOut.Data["clientSecret"])
	}
}
