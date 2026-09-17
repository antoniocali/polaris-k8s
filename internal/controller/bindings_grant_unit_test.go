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
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

func TestPolarisPrincipalRoleBindingReconcile_PutsAssignment(t *testing.T) {
	fp := newFakePolaris(t)
	var put atomic.Bool
	fp.route("PUT", "/api/management/v1/principals/airflow/principal-roles", func(w http.ResponseWriter, r *http.Request) {
		put.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	pp := &polarisv1alpha1.PolarisPrincipal{
		ObjectMeta: metav1.ObjectMeta{Name: "airflow", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisPrincipalSpec{
			ConnectionRef:        polarisv1alpha1.ConnectionRef{Name: "prod"},
			CredentialsSecretRef: polarisv1alpha1.GeneratedCredentialsSecretRef{Name: "airflow-creds"},
		},
		Status: polarisv1alpha1.PolarisPrincipalStatus{Conditions: readyConditions()},
	}
	pr := &polarisv1alpha1.PolarisPrincipalRole{
		ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisPrincipalRoleSpec{ConnectionRef: polarisv1alpha1.ConnectionRef{Name: "prod"}},
		Status:     polarisv1alpha1.PolarisPrincipalRoleStatus{Conditions: readyConditions()},
	}
	b := &polarisv1alpha1.PolarisPrincipalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "a-w", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisPrincipalRoleBindingSpec{
			PrincipalRef:     polarisv1alpha1.PrincipalRef{Name: "airflow"},
			PrincipalRoleRef: polarisv1alpha1.PrincipalRoleRef{Name: "writer"},
		},
	}
	c := newFakeClient(t, conn, secret, pp, pr, b)
	r := &PolarisPrincipalRoleBindingReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "a-w", "data-platform")

	if !put.Load() {
		t.Fatal("PUT /principals/airflow/principal-roles not called")
	}
	got := &polarisv1alpha1.PolarisPrincipalRoleBinding{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "a-w", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
}

// TestPolarisCatalogRoleBindingReconcile_WaitsOnServerSide404 pins the
// Polaris-side 404 backstop: oapi-codegen's WithResponse methods report a 404
// via resp.StatusCode() (err stays nil), so the wait must trigger off the
// status code, not off err. A regression here would surface the 404 as a
// PolarisError with exponential backoff instead of a quiet DependencyNotReady.
func TestPolarisCatalogRoleBindingReconcile_WaitsOnServerSide404(t *testing.T) {
	fp := newFakePolaris(t)
	// Parents are Ready in K8s, but the principal-role/catalog-role don't exist
	// server-side yet → Polaris 404s on assign.
	fp.reply("PUT", "/api/management/v1/principal-roles/writer/catalog-roles/lakehouse", http.StatusNotFound, map[string]string{"error": "not found"})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	pr := &polarisv1alpha1.PolarisPrincipalRole{
		ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisPrincipalRoleSpec{ConnectionRef: polarisv1alpha1.ConnectionRef{Name: "prod"}},
		Status:     polarisv1alpha1.PolarisPrincipalRoleStatus{Conditions: readyConditions()},
	}
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}, Name: "rw"},
		Status:     polarisv1alpha1.PolarisCatalogRoleStatus{Conditions: readyConditions()},
	}
	b := &polarisv1alpha1.PolarisCatalogRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "w-rw", Namespace: "data-platform", Finalizers: []string{PolarisFinalizer}},
		Spec: polarisv1alpha1.PolarisCatalogRoleBindingSpec{
			PrincipalRoleRef: polarisv1alpha1.PrincipalRoleRef{Name: "writer"},
			CatalogRoleRef:   polarisv1alpha1.CatalogRoleRef{Name: "rw"},
		},
	}
	c := newFakeClient(t, conn, secret, cat, pr, cr, b)
	r := &PolarisCatalogRoleBindingReconciler{Client: c, Scheme: c.Scheme(), BuildPolarisClient: fakeBuilder(t, fp)}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "w-rw", Namespace: "data-platform"},
	})
	if err != nil {
		t.Fatalf("a server-side 404 should be a quiet wait, got error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected a RequeueAfter dependency wait, got %+v", res)
	}

	got := &polarisv1alpha1.PolarisCatalogRoleBinding{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "w-rw", Namespace: "data-platform"}, got); err != nil {
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

func TestPolarisCatalogRoleBindingReconcile_PutsAssignment(t *testing.T) {
	fp := newFakePolaris(t)
	var put atomic.Bool
	fp.route("PUT", "/api/management/v1/principal-roles/writer/catalog-roles/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		put.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	pr := &polarisv1alpha1.PolarisPrincipalRole{
		ObjectMeta: metav1.ObjectMeta{Name: "writer", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisPrincipalRoleSpec{ConnectionRef: polarisv1alpha1.ConnectionRef{Name: "prod"}},
		Status:     polarisv1alpha1.PolarisPrincipalRoleStatus{Conditions: readyConditions()},
	}
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}, Name: "rw"},
		Status:     polarisv1alpha1.PolarisCatalogRoleStatus{Conditions: readyConditions()},
	}
	b := &polarisv1alpha1.PolarisCatalogRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "w-rw", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisCatalogRoleBindingSpec{
			PrincipalRoleRef: polarisv1alpha1.PrincipalRoleRef{Name: "writer"},
			CatalogRoleRef:   polarisv1alpha1.CatalogRoleRef{Name: "rw"},
		},
	}
	c := newFakeClient(t, conn, secret, cat, pr, cr, b)
	r := &PolarisCatalogRoleBindingReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "w-rw", "data-platform")

	if !put.Load() {
		t.Fatal("PUT /principal-roles/writer/catalog-roles/lakehouse not called")
	}
}

func TestPolarisGrantReconcile_NamespaceTarget(t *testing.T) {
	fp := newFakePolaris(t)
	var seenBody atomic.Pointer[map[string]any]
	fp.route("PUT", "/api/management/v1/catalogs/lakehouse/catalog-roles/rw/grants", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		seenBody.Store(&decoded)
		w.WriteHeader(http.StatusNoContent)
	})

	const nsName = "analytics"
	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}},
		Status:     polarisv1alpha1.PolarisCatalogRoleStatus{Conditions: readyConditions()},
	}
	ns := makeNamespace(nsName, "data-platform", "lakehouse", "")
	g := &polarisv1alpha1.PolarisGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "rw-analytics", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisGrantSpec{
			CatalogRoleRef: polarisv1alpha1.CatalogRoleRef{Name: "rw"},
			Privilege:      polarisv1alpha1.Privilege("TABLE_WRITE_DATA"),
			Target: polarisv1alpha1.GrantTarget{
				Type:         polarisv1alpha1.GrantTargetNamespace,
				NamespaceRef: &polarisv1alpha1.NamespaceRef{Name: nsName},
			},
		},
	}
	c := newFakeClient(t, conn, secret, cat, cr, ns, g)
	r := &PolarisGrantReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "rw-analytics", "data-platform")

	bodyPtr := seenBody.Load()
	if bodyPtr == nil {
		t.Fatal("PUT /grants not called")
	}
	grant, ok := (*bodyPtr)["grant"].(map[string]any)
	if !ok {
		t.Fatalf("body.grant not an object: %v", *bodyPtr)
	}
	if grant["type"] != "namespace" || grant["privilege"] != "TABLE_WRITE_DATA" {
		t.Errorf("grant.type/privilege wrong: %+v", grant)
	}
	pathRaw, _ := grant["namespace"].([]any)
	if len(pathRaw) != 1 || pathRaw[0] != nsName {
		t.Errorf("grant.namespace=%v, want [%s]", pathRaw, nsName)
	}
}

func TestPolarisGrantReconcile_RejectsMissingNamespaceRef(t *testing.T) {
	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cr := &polarisv1alpha1.PolarisCatalogRole{
		ObjectMeta: metav1.ObjectMeta{Name: "rw", Namespace: "data-platform"},
		Spec:       polarisv1alpha1.PolarisCatalogRoleSpec{CatalogRef: polarisv1alpha1.CatalogRef{Name: "lakehouse"}},
		Status:     polarisv1alpha1.PolarisCatalogRoleStatus{Conditions: readyConditions()},
	}
	g := &polarisv1alpha1.PolarisGrant{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-grant", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisGrantSpec{
			CatalogRoleRef: polarisv1alpha1.CatalogRoleRef{Name: "rw"},
			Privilege:      polarisv1alpha1.Privilege("TABLE_WRITE_DATA"),
			Target:         polarisv1alpha1.GrantTarget{Type: polarisv1alpha1.GrantTargetNamespace}, // no namespaceRef
		},
	}
	c := newFakeClient(t, conn, secret, cat, cr, g)
	r := &PolarisGrantReconciler{Client: c, Scheme: c.Scheme()}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "bad-grant", Namespace: "data-platform"},
	}); err == nil {
		t.Fatal("Reconcile: expected invalid-spec error, got nil")
	}
}
