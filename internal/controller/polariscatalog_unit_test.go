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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// reconcileCatalogUntilSynced runs the reconciler until it either
// completes without requesting a requeue or hits an error. Each successful
// reconcile path either creates/updates/no-ops and then sets status, or
// adds a finalizer and asks for a requeue.
//
//nolint:unparam,staticcheck // ns generic for future tests; SA1019 on Result.Requeue is intentional
func reconcileCatalogUntilSynced(t *testing.T, r *PolarisCatalogReconciler, name, ns string) {
	t.Helper()
	for i := range 5 {
		res, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
		})
		if err != nil {
			t.Fatalf("Reconcile[%d]: %v", i, err)
		}
		if !res.Requeue && res.RequeueAfter == 0 {
			return
		}
	}
	t.Fatalf("Reconcile did not settle after 5 iterations")
}

func TestPolarisCatalogReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)

	var createBody atomic.Pointer[map[string]any]
	fp.reply("GET", "/api/management/v1/catalogs/lakehouse", http.StatusNotFound, map[string]string{"error": "not found"})
	fp.route("POST", "/api/management/v1/catalogs", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		createBody.Store(&decoded)
		w.WriteHeader(http.StatusCreated)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	c := newFakeClient(t, conn, secret, cat)

	r := &PolarisCatalogReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileCatalogUntilSynced(t, r, "lakehouse", "data-platform")

	got := &polarisv1alpha1.PolarisCatalog{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
	if !controllerutil.ContainsFinalizer(got, PolarisFinalizer) {
		t.Errorf("finalizer not added; finalizers=%v", got.Finalizers)
	}
	if got.Status.PolarisCatalogID != "lakehouse" {
		t.Errorf("PolarisCatalogID=%q, want %q", got.Status.PolarisCatalogID, "lakehouse")
	}

	// Verify the create body actually carried the S3 fields.
	bodyPtr := createBody.Load()
	if bodyPtr == nil {
		t.Fatal("create endpoint was not called")
	}
	catalogBody, ok := (*bodyPtr)["catalog"].(map[string]any)
	if !ok {
		t.Fatalf("body.catalog not an object: %v", *bodyPtr)
	}
	storage, ok := catalogBody["storageConfigInfo"].(map[string]any)
	if !ok {
		t.Fatalf("body.catalog.storageConfigInfo not an object: %v", catalogBody)
	}
	if storage["storageType"] != "S3" {
		t.Errorf("storageType=%v, want S3", storage["storageType"])
	}
	if storage["roleArn"] != "arn:aws:iam::123456789012:role/polaris" {
		t.Errorf("roleArn=%v, want the S3 ARN", storage["roleArn"])
	}
}

func TestPolarisCatalogReconcile_UpdatesOnDrift(t *testing.T) {
	fp := newFakePolaris(t)

	// GET returns a catalog with stale properties (no `team` key).
	fp.route("GET", "/api/management/v1/catalogs/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":          "lakehouse",
			"type":          "INTERNAL",
			"entityVersion": 7,
			"properties": map[string]any{
				"default-base-location": "s3://bucket/lakehouse",
			},
			"storageConfigInfo": map[string]any{
				"storageType":      "S3",
				"allowedLocations": []string{"s3://bucket/lakehouse"},
			},
		})
	})

	var updateCalled atomic.Bool
	var updateVersion atomic.Int64
	fp.route("PUT", "/api/management/v1/catalogs/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		updateCalled.Store(true)
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if v, ok := got["currentEntityVersion"].(float64); ok {
			updateVersion.Store(int64(v))
		}
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cat.Spec.Properties = map[string]string{"team": "data-platform"} // drift vs. observed
	cat.Finalizers = []string{PolarisFinalizer}                      // skip the finalizer requeue
	c := newFakeClient(t, conn, secret, cat)

	r := &PolarisCatalogReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileCatalogUntilSynced(t, r, "lakehouse", "data-platform")

	if !updateCalled.Load() {
		t.Fatal("PUT /catalogs/lakehouse was not called — drift not detected")
	}
	if updateVersion.Load() != 7 {
		t.Errorf("updateCatalog body.currentEntityVersion=%d, want 7", updateVersion.Load())
	}
}

func TestPolarisCatalogReconcile_NoUpdateWhenInSync(t *testing.T) {
	fp := newFakePolaris(t)

	fp.route("GET", "/api/management/v1/catalogs/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":       "lakehouse",
			"type":       "INTERNAL",
			"properties": map[string]any{"default-base-location": "s3://bucket/lakehouse"},
			"storageConfigInfo": map[string]any{
				"storageType":      "S3",
				"allowedLocations": []string{"s3://bucket/lakehouse"},
			},
		})
	})

	var updateCalled atomic.Bool
	fp.route("PUT", "/api/management/v1/catalogs/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		updateCalled.Store(true)
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cat.Finalizers = []string{PolarisFinalizer}
	c := newFakeClient(t, conn, secret, cat)

	r := &PolarisCatalogReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileCatalogUntilSynced(t, r, "lakehouse", "data-platform")

	if updateCalled.Load() {
		t.Error("PUT /catalogs/lakehouse was called despite no drift")
	}
}

func TestPolarisCatalogReconcile_DeletesOnFinalize(t *testing.T) {
	fp := newFakePolaris(t)

	var deleteCalled atomic.Bool
	fp.route("DELETE", "/api/management/v1/catalogs/lakehouse", func(w http.ResponseWriter, r *http.Request) {
		deleteCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cat.Finalizers = []string{PolarisFinalizer}
	now := metav1.Now()
	cat.DeletionTimestamp = &now
	c := newFakeClient(t, conn, secret, cat)

	r := &PolarisCatalogReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !deleteCalled.Load() {
		t.Error("DELETE /catalogs/lakehouse was not called during finalize")
	}

	got := &polarisv1alpha1.PolarisCatalog{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"}, got)
	// Once the finalizer is gone, the fake client should garbage-collect the
	// object — either we get NotFound or the finalizer slice is empty.
	if err == nil && controllerutil.ContainsFinalizer(got, PolarisFinalizer) {
		t.Errorf("finalizer still present after finalize: %v", got.Finalizers)
	}
}

// TestPolarisCatalogReconcile_FinalizesWhenConnectionGone guards the systemic
// stuck-finalizer fix: when an object is being deleted but its
// PolarisConnection (or credentials Secret) has already been removed — common
// during `kubectl delete namespace`, where deletion order is nondeterministic
// — the reconciler must treat the remote as unreachable and drop the
// finalizer rather than erroring forever and wedging the object in
// Terminating.
func TestPolarisCatalogReconcile_FinalizesWhenConnectionGone(t *testing.T) {
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	cat.Finalizers = []string{PolarisFinalizer}
	now := metav1.Now()
	cat.DeletionTimestamp = &now
	c := newFakeClient(t, cat) // no Connection, no Secret — both already deleted

	r := &PolarisCatalogReconciler{
		Client: c,
		Scheme: c.Scheme(),
		// No BuildPolarisClient: getConnection fails before the client is built.
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"},
	}); err != nil {
		t.Fatalf("Reconcile on delete path with missing connection should not error, got: %v", err)
	}

	got := &polarisv1alpha1.PolarisCatalog{}
	err := c.Get(context.Background(), types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"}, got)
	// Finalizer dropped → fake client GCs the object (NotFound) or the slice is empty.
	if err == nil && controllerutil.ContainsFinalizer(got, PolarisFinalizer) {
		t.Errorf("finalizer still present — object wedged in Terminating; finalizers=%v", got.Finalizers)
	}
}

// TestBuildStorageBody_File covers the local-filesystem storage type
// (testing only). FILE carries no cloud-specific fields — just storageType
// and allowedLocations — so it must build cleanly without an s3/azure/gcs
// sub-block.
func TestBuildStorageBody_File(t *testing.T) {
	out, err := buildStorageBody(polarisv1alpha1.StorageConfig{
		StorageType:      polarisv1alpha1.StorageTypeFile,
		AllowedLocations: []string{"file:///tmp/polaris/lakehouse"},
	})
	if err != nil {
		t.Fatalf("buildStorageBody(FILE): %v", err)
	}
	if out.StorageType != "FILE" {
		t.Errorf("storageType=%q, want FILE", out.StorageType)
	}
	if len(out.AllowedLocations) != 1 || out.AllowedLocations[0] != "file:///tmp/polaris/lakehouse" {
		t.Errorf("allowedLocations=%v, want the file URI", out.AllowedLocations)
	}
	if out.RoleARN != "" || out.TenantID != "" || out.GCSServiceAccount != "" {
		t.Errorf("FILE body leaked cloud-specific fields: %+v", out)
	}
}

// TestBuildCatalogCreateBody_File confirms a FILE catalog marshals without any
// cloud-specific keys leaking onto the wire (omitempty drops them).
func TestBuildCatalogCreateBody_File(t *testing.T) {
	cat := makeCatalog("local", "dev", "prod")
	cat.Spec.DefaultBaseLocation = "file:///tmp/polaris/local"
	cat.Spec.StorageConfig = polarisv1alpha1.StorageConfig{
		StorageType:      polarisv1alpha1.StorageTypeFile,
		AllowedLocations: []string{"file:///tmp/polaris/local"},
	}
	body, err := buildCatalogCreateBody(cat, "local")
	if err != nil {
		t.Fatalf("buildCatalogCreateBody(FILE): %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	storage := decoded["catalog"].(map[string]any)["storageConfigInfo"].(map[string]any)
	if storage["storageType"] != "FILE" {
		t.Errorf("storageType=%v, want FILE", storage["storageType"])
	}
	if _, ok := storage["roleArn"]; ok {
		t.Errorf("FILE body must not carry roleArn: %v", storage)
	}
}

func TestPolarisCatalogReconcile_MissingConnection(t *testing.T) {
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	c := newFakeClient(t, cat) // no Connection

	r := &PolarisCatalogReconciler{
		Client: c,
		Scheme: c.Scheme(),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"},
	}); err == nil {
		t.Fatal("Reconcile: expected ref-error, got nil")
	}

	got := &polarisv1alpha1.PolarisCatalog{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "lakehouse", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionFalse) {
		t.Errorf("Ready=False not set:%s", dumpConditions(got.Status.Conditions))
	}
}
