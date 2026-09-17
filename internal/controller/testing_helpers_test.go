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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	polarisv1alpha1 "github.com/antoniocali/polaris-k8s/api/v1alpha1"
	"github.com/antoniocali/polaris-k8s/internal/polaris"
)

// testScheme is shared across all reconciler unit tests. It carries the
// core/v1 types (for Secret writes), apps if we ever need them, and the
// polaris-k8s API group.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := polarisv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add polaris scheme: %v", err)
	}
	return s
}

// newFakeClient builds a controller-runtime fake client with the supplied
// initial objects and status-subresource enabled for every polaris-k8s kind
// (the fake client needs an explicit list).
func newFakeClient(t *testing.T, initObjs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithObjects(initObjs...).
		WithStatusSubresource(
			&polarisv1alpha1.PolarisConnection{},
			&polarisv1alpha1.PolarisCatalog{},
			&polarisv1alpha1.PolarisNamespace{},
			&polarisv1alpha1.PolarisTable{},
			&polarisv1alpha1.PolarisView{},
			&polarisv1alpha1.PolarisPolicy{},
			&polarisv1alpha1.PolarisPrincipal{},
			&polarisv1alpha1.PolarisPrincipalRole{},
			&polarisv1alpha1.PolarisCatalogRole{},
			&polarisv1alpha1.PolarisPrincipalRoleBinding{},
			&polarisv1alpha1.PolarisCatalogRoleBinding{},
			&polarisv1alpha1.PolarisGrant{},
		).
		Build()
}

// fakePolaris is a minimal, configurable httptest server that pretends to
// be a Polaris instance for reconciler unit tests. Each test can override
// the handlers for the endpoints it cares about.
type fakePolaris struct {
	srv *httptest.Server

	mu       sync.Mutex
	handlers map[string]http.HandlerFunc

	tokenCalls atomic.Int32
}

func newFakePolaris(t *testing.T) *fakePolaris {
	t.Helper()
	fp := &fakePolaris{handlers: map[string]http.HandlerFunc{}}

	// Single dispatcher so per-route overrides via route() always win,
	// including for the token endpoint. The default token handler is
	// installed into fp.handlers so a test can replace it.
	fp.route("POST", polaris.DefaultTokenPath, func(w http.ResponseWriter, r *http.Request) {
		fp.tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		h, ok := fp.handlers[r.Method+" "+r.URL.Path]
		fp.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	})

	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

// route registers a handler at "<method> <path>". The path must include
// the full API prefix (e.g. /api/management/v1/catalogs).
func (fp *fakePolaris) route(method, path string, h http.HandlerFunc) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.handlers[method+" "+path] = h
}

// reply registers a handler that responds with the given status and JSON
// body for every call to the route. When body is nil, no Content-Type is
// set — that matters because oapi-codegen's wrappers try to unmarshal any
// response whose Content-Type contains "json" and a recognised status code,
// which would choke on an empty body.
func (fp *fakePolaris) reply(method, path string, status int, body any) {
	fp.route(method, path, func(w http.ResponseWriter, r *http.Request) {
		if body == nil {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
}

// URL returns the base URL for the fake server.
func (fp *fakePolaris) URL() string { return fp.srv.URL }

// newClientForFake returns a *polaris.Client pointed at the fake server,
// pre-authenticated.
func newClientForFake(t *testing.T, fp *fakePolaris) *polaris.Client {
	t.Helper()
	c, err := polaris.NewClient(polaris.Config{
		ServerURL:    fp.URL(),
		ClientID:     "id",
		ClientSecret: "secret",
	})
	if err != nil {
		t.Fatalf("polaris.NewClient: %v", err)
	}
	return c
}

// fakeBuilder returns a clientBuilderFunc that always yields a client
// pointed at the given fake server, regardless of which PolarisConnection
// is passed.
func fakeBuilder(t *testing.T, fp *fakePolaris) clientBuilderFunc {
	t.Helper()
	return func(_ context.Context, _ client.Client, _ *polarisv1alpha1.PolarisConnection) (*polaris.Client, error) {
		return newClientForFake(t, fp), nil
	}
}

// --- CR factory helpers ---
//
// The factories take name+namespace params so individual tests can vary
// them as scenarios grow. The lint warning that "name always receives X"
// is the cost of having the knobs available; suppressed per-helper.

//nolint:unparam // factory kept generic
func makeConnection(name, ns string) *polarisv1alpha1.PolarisConnection {
	return &polarisv1alpha1.PolarisConnection{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: polarisv1alpha1.PolarisConnectionSpec{
			ServerURL: "https://polaris.example",
			CredentialsSecretRef: polarisv1alpha1.ClientCredentialsSecretRef{
				Name: name + "-creds",
			},
		},
		Status: polarisv1alpha1.PolarisConnectionStatus{Conditions: readyConditions()},
	}
}

//nolint:unparam // factory kept generic
func makeCredentialsSecret(connName, ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: connName + "-creds", Namespace: ns},
		Data: map[string][]byte{
			"clientId":     []byte("id"),
			"clientSecret": []byte("secret"),
		},
	}
}

//nolint:unparam // factory kept generic
func makeCatalog(name, ns, connName string) *polarisv1alpha1.PolarisCatalog {
	return &polarisv1alpha1.PolarisCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: polarisv1alpha1.PolarisCatalogSpec{
			ConnectionRef:       polarisv1alpha1.ConnectionRef{Name: connName},
			Type:                polarisv1alpha1.CatalogTypeInternal,
			DefaultBaseLocation: "s3://bucket/" + name,
			StorageConfig: polarisv1alpha1.StorageConfig{
				StorageType:      polarisv1alpha1.StorageTypeS3,
				AllowedLocations: []string{"s3://bucket/" + name},
				S3: &polarisv1alpha1.S3StorageConfig{
					RoleARN: "arn:aws:iam::123456789012:role/polaris",
					Region:  "eu-west-1",
				},
			},
		},
		Status: polarisv1alpha1.PolarisCatalogStatus{Conditions: readyConditions()},
	}
}

//nolint:unparam // factory kept generic
func makeNamespace(name, ns, catName string, parentName string) *polarisv1alpha1.PolarisNamespace {
	out := &polarisv1alpha1.PolarisNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: polarisv1alpha1.PolarisNamespaceSpec{
			CatalogRef: polarisv1alpha1.CatalogRef{Name: catName},
		},
	}
	if parentName != "" {
		out.Spec.ParentRef = &polarisv1alpha1.NamespaceRef{Name: parentName}
	}
	out.Status.Conditions = readyConditions()
	return out
}

// readyConditions returns a status condition slice with Ready=True. Test
// fixtures use it to stand in for a parent that has already been reconciled,
// so dependent reconcilers (which now gate on parent readiness) proceed
// instead of waiting.
func readyConditions() []metav1.Condition {
	return []metav1.Condition{{
		Type:               ConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             ReasonReady,
		Message:            "ready (test fixture)",
		LastTransitionTime: metav1.Now(),
	}}
}

// gotConditions returns true when the named condition exists on the object
// with the given status. Used by tests to assert reconciliation outcomes.
func gotCondition(conds []metav1.Condition, typ string, status metav1.ConditionStatus) bool {
	for _, c := range conds {
		if c.Type == typ && c.Status == status {
			return true
		}
	}
	return false
}

// dumpConditions formats conditions for human-readable test failure output.
func dumpConditions(conds []metav1.Condition) string {
	var b strings.Builder
	for _, c := range conds {
		fmt.Fprintf(&b, "\n  - %s=%s reason=%s msg=%q gen=%d", c.Type, c.Status, c.Reason, c.Message, c.ObservedGeneration)
	}
	return b.String()
}
