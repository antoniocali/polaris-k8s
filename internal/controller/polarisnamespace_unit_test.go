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
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
)

//nolint:unparam,staticcheck // ns generic for future tests; SA1019 on Result.Requeue is intentional
func reconcileNamespaceUntilSynced(t *testing.T, r *PolarisNamespaceReconciler, name, ns string) {
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
	t.Fatalf("Reconcile did not settle")
}

// nsURLPath builds the URL path the catalog client will use for the
// supplied namespace path. The Iceberg unit separator (\x1F) URL-encodes
// to %1F, but Go's http.NewServeMux normalises paths so we match what the
// fake server actually sees: each segment URL-escaped and joined.
//
//nolint:unparam // test helper kept generic so future tests can target other catalogs
func nsURLPath(catalog string, segments []string) string {
	joined := strings.Join(segments, "\x1f")
	return "/api/catalog/v1/" + catalog + "/namespaces/" + joined
}

func TestPolarisNamespaceReconcile_CreatesTopLevel(t *testing.T) {
	fp := newFakePolaris(t)

	// Polaris-side: nothing exists.
	fp.reply("GET", nsURLPath("lakehouse", []string{"analytics"}), http.StatusNotFound, nil)

	var createdNS atomic.Pointer[[]any]
	fp.route("POST", "/api/catalog/v1/lakehouse/namespaces", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if ns, ok := got["namespace"].([]any); ok {
			createdNS.Store(&ns)
		}
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	c := newFakeClient(t, conn, secret, cat, ns)

	r := &PolarisNamespaceReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileNamespaceUntilSynced(t, r, "analytics", "data-platform")

	got := &polarisv1alpha1.PolarisNamespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "analytics", Namespace: "data-platform"}, got); err != nil {
		t.Fatalf("re-fetch: %v", err)
	}
	if !gotCondition(got.Status.Conditions, ConditionReady, metav1.ConditionTrue) {
		t.Fatalf("Ready=True not set:%s", dumpConditions(got.Status.Conditions))
	}
	if !reflect.DeepEqual(got.Status.FullPath, []string{"analytics"}) {
		t.Errorf("FullPath=%v, want [analytics]", got.Status.FullPath)
	}
	bodyPtr := createdNS.Load()
	if bodyPtr == nil || len(*bodyPtr) != 1 || (*bodyPtr)[0] != "analytics" {
		t.Errorf("create body namespace=%v, want [analytics]", bodyPtr)
	}
}

func TestPolarisNamespaceReconcile_AssemblesNestedPath(t *testing.T) {
	fp := newFakePolaris(t)

	want := []string{"analytics", "sales"}
	fp.reply("GET", nsURLPath("lakehouse", want), http.StatusNotFound, nil)
	var got atomic.Pointer[[]any]
	fp.route("POST", "/api/catalog/v1/lakehouse/namespaces", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		if ns, ok := decoded["namespace"].([]any); ok {
			got.Store(&ns)
		}
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	parent := makeNamespace("analytics", "data-platform", "lakehouse", "")
	parent.Finalizers = []string{PolarisFinalizer}
	child := makeNamespace("sales", "data-platform", "lakehouse", "analytics")
	c := newFakeClient(t, conn, secret, cat, parent, child)

	r := &PolarisNamespaceReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileNamespaceUntilSynced(t, r, "sales", "data-platform")

	out := got.Load()
	if out == nil {
		t.Fatal("create not called")
	}
	if len(*out) != 2 || (*out)[0] != "analytics" || (*out)[1] != "sales" {
		t.Errorf("create body namespace=%v, want [analytics sales]", *out)
	}
}

func TestPolarisNamespaceReconcile_RejectsCrossCatalogParent(t *testing.T) {
	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	catA := makeCatalog("cat-a", "data-platform", "prod")
	catB := makeCatalog("cat-b", "data-platform", "prod")
	// Parent in cat-a, child claims to be in cat-b — invariant violation.
	parent := makeNamespace("analytics", "data-platform", "cat-a", "")
	parent.Finalizers = []string{PolarisFinalizer}
	child := &polarisv1alpha1.PolarisNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "sales", Namespace: "data-platform"},
		Spec: polarisv1alpha1.PolarisNamespaceSpec{
			CatalogRef: polarisv1alpha1.CatalogRef{Name: "cat-b"},
			ParentRef:  &polarisv1alpha1.NamespaceRef{Name: "analytics"},
		},
	}
	c := newFakeClient(t, conn, secret, catA, catB, parent, child)

	r := &PolarisNamespaceReconciler{
		Client: c,
		Scheme: c.Scheme(),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sales", Namespace: "data-platform"},
	}); err == nil {
		t.Fatal("Reconcile: expected cross-catalog error, got nil")
	}
}

func TestPolarisNamespaceReconcile_RemovesDriftedKeys(t *testing.T) {
	fp := newFakePolaris(t)

	fp.route("GET", nsURLPath("lakehouse", []string{"analytics"}), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"namespace": []string{"analytics"},
			"properties": map[string]string{
				"owner":    "data-platform",
				"legacy":   "should-be-removed",
				"location": "s3://bucket/old",
			},
		})
	})

	var seenUpdates, seenRemovals atomic.Pointer[map[string]any]
	fp.route("POST", nsURLPath("lakehouse", []string{"analytics"})+"/properties", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		if upd, ok := got["updates"].(map[string]any); ok {
			seenUpdates.Store(&upd)
		}
		if rem, ok := got["removals"].([]any); ok {
			conv := map[string]any{"list": rem}
			seenRemovals.Store(&conv)
		}
		w.WriteHeader(http.StatusOK)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	ns.Spec.Properties = map[string]string{
		"owner":    "data-platform",
		"location": "s3://bucket/new", // updated
		// "legacy" not in desired -> should be removed
	}
	ns.Finalizers = []string{PolarisFinalizer}
	c := newFakeClient(t, conn, secret, cat, ns)

	r := &PolarisNamespaceReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	reconcileNamespaceUntilSynced(t, r, "analytics", "data-platform")

	updPtr := seenUpdates.Load()
	remPtr := seenRemovals.Load()
	if updPtr == nil {
		t.Fatal("updateProperties was not called or had no updates")
	}
	if (*updPtr)["location"] != "s3://bucket/new" {
		t.Errorf("updates.location=%v, want s3://bucket/new", (*updPtr)["location"])
	}
	if remPtr == nil {
		t.Fatal("updateProperties was not called with removals")
	}
	list, _ := (*remPtr)["list"].([]any)
	got := make([]string, 0, len(list))
	for _, v := range list {
		got = append(got, v.(string))
	}
	sort.Strings(got)
	if len(got) != 1 || got[0] != "legacy" {
		t.Errorf("removals=%v, want [legacy]", got)
	}
}

func TestPolarisNamespaceReconcile_DropOnFinalize(t *testing.T) {
	fp := newFakePolaris(t)

	var dropCalled atomic.Bool
	fp.route("DELETE", nsURLPath("lakehouse", []string{"analytics"}), func(w http.ResponseWriter, r *http.Request) {
		dropCalled.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})

	conn := makeConnection("prod", "data-platform")
	secret := makeCredentialsSecret("prod", "data-platform")
	cat := makeCatalog("lakehouse", "data-platform", "prod")
	ns := makeNamespace("analytics", "data-platform", "lakehouse", "")
	ns.Finalizers = []string{PolarisFinalizer}
	now := metav1.Now()
	ns.DeletionTimestamp = &now
	c := newFakeClient(t, conn, secret, cat, ns)

	r := &PolarisNamespaceReconciler{
		Client:             c,
		Scheme:             c.Scheme(),
		BuildPolarisClient: fakeBuilder(t, fp),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "analytics", Namespace: "data-platform"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !dropCalled.Load() {
		t.Error("DELETE on namespace was not called")
	}
}
