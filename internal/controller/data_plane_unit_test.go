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
	"strings"
	"sync/atomic"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

// TestBuildIcebergSchema_PrimitiveTypeSerializesAsString is a regression test:
// a primitive Iceberg type must serialize as a bare JSON string (e.g. "long"),
// not an object. buildIcebergSchema used to call the generated union type's
// MergePrimitiveType, which runs the value through an object-merge helper —
// fine for the struct/list/map variants, but a primitive has no object to
// merge into, so it silently produced "{}" for the type field. Real Polaris
// rejects that ("Cannot parse type from json: {}"); the fake Polaris server
// used by every other unit test here doesn't validate body content at all,
// so this went unnoticed until a real envtest/e2e run against real Polaris.
// FromPrimitiveType (a straight assignment) is the correct call.
func TestBuildIcebergSchema_PrimitiveTypeSerializesAsString(t *testing.T) {
	schema, err := buildIcebergSchema(polarisv1alpha1.IcebergSchema{
		Fields: []polarisv1alpha1.IcebergField{
			{ID: 1, Name: "id", Type: "long", Required: true},
		},
	})
	if err != nil {
		t.Fatalf("buildIcebergSchema: %v", err)
	}
	b, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if !strings.Contains(string(b), `"type":"long"`) {
		t.Fatalf("expected field type to serialize as the bare string \"long\", got: %s", b)
	}
}

// TestPolarisTableReconcile_WaitsOnServerSide404 covers the data-plane (Catalog
// client) 404 backstop: the table's namespace doesn't exist server-side yet, so
// CreateTable 404s via resp.StatusCode(). Must be a quiet DependencyNotReady
// wait, not a PolarisError.
func TestPolarisTableReconcile_WaitsOnServerSide404(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/catalog/v1/lakehouse/namespaces/analytics/tables/orders", http.StatusNotFound, nil)
	fp.reply("POST", "/api/catalog/v1/lakehouse/namespaces/analytics/tables", http.StatusNotFound, map[string]any{
		"error": map[string]any{"message": "Namespace does not exist", "type": "NoSuchNamespaceException", "code": 404},
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	tbl := &polarisv1alpha1.PolarisTable{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "data-platform", Finalizers: []string{PolarisFinalizer}},
		Spec: polarisv1alpha1.PolarisTableSpec{
			NamespaceRef: polarisv1alpha1.NamespaceRef{Name: "analytics"},
			Schema: polarisv1alpha1.IcebergSchema{
				Fields: []polarisv1alpha1.IcebergField{{ID: 1, Name: "id", Type: "long", Required: true}},
			},
			WriteFormat: polarisv1alpha1.WriteFormatParquet,
		},
	}
	c := newFakeClient(t, conn, secret, cat, ns, tbl)
	r := &PolarisTableReconciler{Client: c, Scheme: c.Scheme(), BuildPolarisClient: fakeBuilder(t, fp)}

	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "orders", Namespace: "data-platform"},
	})
	if err != nil {
		t.Fatalf("server-side 404 should be a quiet wait, got error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected a RequeueAfter dependency wait, got %+v", res)
	}
	got := &polarisv1alpha1.PolarisTable{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "orders", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionFalse) {
		t.Errorf("want Ready=False, got:%s", dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisTableReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/catalog/v1/lakehouse/namespaces/analytics/tables/orders", http.StatusNotFound, nil)

	var createBody atomic.Pointer[map[string]any]
	fp.route("POST", "/api/catalog/v1/lakehouse/namespaces/analytics/tables", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		createBody.Store(&decoded)
		// CreateTable returns a LoadTableResult.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata-location": "s3://bucket/orders/metadata.json",
			"metadata": map[string]any{
				"format-version": 2,
				"table-uuid":     "uuid-orders",
				"location":       "s3://bucket/orders",
			},
		})
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	ns.Finalizers = []string{PolarisFinalizer}
	tbl := &polarisv1alpha1.PolarisTable{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisTableSpec{
			NamespaceRef: polarisv1alpha1.NamespaceRef{Name: "analytics"},
			Schema: polarisv1alpha1.IcebergSchema{
				Fields: []polarisv1alpha1.IcebergField{
					{ID: 1, Name: "id", Type: "long", Required: true},
					{ID: 2, Name: "amount", Type: "decimal(10,2)"},
				},
			},
			WriteFormat: polarisv1alpha1.WriteFormatParquet,
		},
	}
	c := newFakeClient(t, conn, secret, cat, ns, tbl)

	r := &PolarisTableReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "orders", "data-platform")

	bodyPtr := createBody.Load()
	if bodyPtr == nil {
		t.Fatal("create table POST was not called")
	}
	if (*bodyPtr)["name"] != "orders" {
		t.Errorf("create body name=%v, want orders", (*bodyPtr)["name"])
	}
	props, _ := (*bodyPtr)["properties"].(map[string]any)
	if props["write.format.default"] != "parquet" {
		t.Errorf("write.format.default=%v, want parquet", props["write.format.default"])
	}
	got := &polarisv1alpha1.PolarisTable{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "orders", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Errorf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
}

func TestPolarisViewReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/catalog/v1/lakehouse/namespaces/analytics/views/orders_v", http.StatusNotFound, nil)
	var createBody atomic.Pointer[map[string]any]
	fp.route("POST", "/api/catalog/v1/lakehouse/namespaces/analytics/views", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		createBody.Store(&decoded)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"metadata-location": "s3://bucket/orders_v/metadata.json",
			"metadata": map[string]any{
				"view-uuid":          "uuid-orders-v",
				"format-version":     1,
				"location":           "s3://bucket/orders_v",
				"current-version-id": 1,
				"schemas":            []any{},
				"versions":           []any{},
				"version-log":        []any{},
			},
		})
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	ns.Finalizers = []string{PolarisFinalizer}
	view := &polarisv1alpha1.PolarisView{
		ObjectMeta: metav1.ObjectMeta{Name: "orders_v", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisViewSpec{
			NamespaceRef: polarisv1alpha1.NamespaceRef{Name: "analytics"},
			Schema: polarisv1alpha1.IcebergSchema{
				Fields: []polarisv1alpha1.IcebergField{
					{ID: 1, Name: "id", Type: "long"},
				},
			},
			SQL:     "SELECT id FROM orders",
			Dialect: "spark",
		},
	}
	c := newFakeClient(t, conn, secret, cat, ns, view)

	r := &PolarisViewReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "orders_v", "data-platform")

	bodyPtr := createBody.Load()
	if bodyPtr == nil {
		t.Fatal("create view POST was not called")
	}
	vv, _ := (*bodyPtr)["view-version"].(map[string]any)
	reps, _ := vv["representations"].([]any)
	if len(reps) != 1 {
		t.Fatalf("view-version.representations=%v, want 1 entry", reps)
	}
	rep0, _ := reps[0].(map[string]any)
	if rep0["sql"] != "SELECT id FROM orders" {
		t.Errorf("rep.sql=%v", rep0["sql"])
	}
	if rep0["dialect"] != "spark" {
		t.Errorf("rep.dialect=%v, want spark", rep0["dialect"])
	}
}

func TestPolarisPolicyReconcile_CreatesWhenMissing(t *testing.T) {
	fp := newFakePolaris(t)
	fp.reply("GET", "/api/catalog/polaris/v1/lakehouse/namespaces/analytics/policies/compact", http.StatusNotFound, nil)
	var createBody atomic.Pointer[map[string]any]
	fp.route("POST", "/api/catalog/polaris/v1/lakehouse/namespaces/analytics/policies", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		createBody.Store(&decoded)
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	ns.Finalizers = []string{PolarisFinalizer}
	pol := &polarisv1alpha1.PolarisPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "compact", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisPolicySpec{
			NamespaceRef: polarisv1alpha1.NamespaceRef{Name: "analytics"},
			Type:         "system.data-compaction",
			Content:      apiextensionsv1.JSON{Raw: []byte(`{"target_file_size_mb":256}`)},
		},
	}
	c := newFakeClient(t, conn, secret, cat, ns, pol)

	r := &PolarisPolicyReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileUntilSynced(t, r.Reconcile, "compact", "data-platform")

	bodyPtr := createBody.Load()
	if bodyPtr == nil {
		t.Fatal("create policy POST was not called")
	}
	if (*bodyPtr)["type"] != "system.data-compaction" {
		t.Errorf("type=%v, want system.data-compaction", (*bodyPtr)["type"])
	}
	if (*bodyPtr)["content"] != `{"target_file_size_mb":256}` {
		t.Errorf("content=%v", (*bodyPtr)["content"])
	}
}
