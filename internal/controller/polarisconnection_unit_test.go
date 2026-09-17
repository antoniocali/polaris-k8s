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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

func TestPolarisConnectionReconcile_Healthy(t *testing.T) {
	fp := newFakePolaris(t)
	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	c := newFakeClient(t, conn, secret)

	r := &PolarisConnectionReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "prod", Namespace: "data-platform"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	got := &polarisv1alpha1.PolarisConnection{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
	if !gotCondition(got.Status.Conditions, ConditionAuthValid, metav1.ConditionTrue) {
		t.Errorf("AuthValid=True not set:%s", dumpConditions(got.Status.Conditions))
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Errorf("observedGeneration=%d, generation=%d", got.Status.ObservedGeneration, got.Generation)
	}
}

func TestPolarisConnectionReconcile_MissingSecret(t *testing.T) {
	conn := makeConnection("prod", "data-platform")
	// No credentials Secret — buildPolarisClient should fail.
	c := newFakeClient(t, conn)

	r := &PolarisConnectionReconciler{
		Client: c,
		Scheme: c.Scheme(),
		// Use the production builder so we exercise the real Secret lookup.
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "prod", Namespace: "data-platform"},
	})
	if err == nil {
		t.Fatal("Reconcile: expected error from missing Secret, got nil")
	}

	got := &polarisv1alpha1.PolarisConnection{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionFalse) {
		t.Errorf("Ready=False not set:%s", dumpConditions(got.Status.Conditions))
	}
	if !gotCondition(got.Status.Conditions, ConditionAuthValid, metav1.ConditionFalse) {
		t.Errorf("AuthValid=False not set:%s", dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisConnectionReconcile_BadCreds(t *testing.T) {
	fp := newFakePolaris(t)
	// Override the token endpoint to reject any request.
	fp.route("POST", "/api/catalog/v1/oauth/tokens", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_client", http.StatusUnauthorized)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	c := newFakeClient(t, conn, secret)

	r := &PolarisConnectionReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "prod", Namespace: "data-platform"},
	}); err == nil {
		t.Fatal("Reconcile: expected auth error, got nil")
	}

	got := &polarisv1alpha1.PolarisConnection{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "prod", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionAuthValid, metav1.ConditionFalse) {
		t.Errorf("AuthValid=False not set:%s", dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisConnectionReconcile_NotFound(t *testing.T) {
	// Reconciling a non-existent CR is the standard "deleted between watch
	// and reconcile" case — should be a clean no-op.
	c := newFakeClient(t)
	r := &PolarisConnectionReconciler{Client: c, Scheme: c.Scheme()}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "ghost", Namespace: "data-platform"},
	})
	if err != nil || res.Requeue || res.RequeueAfter != 0 { //nolint:staticcheck // SA1019 on Result.Requeue is intentional
		t.Fatalf("Reconcile on missing CR: result=%+v err=%v", res, err)
	}
}
