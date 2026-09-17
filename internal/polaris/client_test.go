/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package polaris

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakePolaris is a minimal httptest server that pretends to be a Polaris
// instance: it accepts client_credentials at the token path and tracks
// the number of times each path has been called.
type fakePolaris struct {
	srv *httptest.Server

	tokenCalls atomic.Int32
	mgmtCalls  atomic.Int32

	tokenStatus    int
	tokenBody      string
	tokenExpiresIn int

	mgmtStatus int
	mgmtBody   string
}

func newFakePolaris(t *testing.T) *fakePolaris {
	t.Helper()
	fp := &fakePolaris{
		tokenStatus:    http.StatusOK,
		tokenExpiresIn: 3600,
		mgmtStatus:     http.StatusOK,
		mgmtBody:       `{"catalogs":[]}`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(DefaultTokenPath, func(w http.ResponseWriter, r *http.Request) {
		fp.tokenCalls.Add(1)
		if fp.tokenStatus != http.StatusOK {
			http.Error(w, fp.tokenBody, fp.tokenStatus)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "unsupported grant_type", http.StatusBadRequest)
			return
		}
		body := tokenResponse{
			AccessToken: fmt.Sprintf("tok-%d", fp.tokenCalls.Load()),
			TokenType:   "Bearer",
			ExpiresIn:   fp.tokenExpiresIn,
			Scope:       r.Form.Get("scope"),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("/api/management/v1/catalogs", func(w http.ResponseWriter, r *http.Request) {
		fp.mgmtCalls.Add(1)
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer tok-") {
			http.Error(w, "missing/invalid bearer", http.StatusUnauthorized)
			return
		}
		if fp.mgmtStatus != http.StatusOK {
			http.Error(w, fp.mgmtBody, fp.mgmtStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fp.mgmtBody)
	})
	fp.srv = httptest.NewServer(mux)
	t.Cleanup(fp.srv.Close)
	return fp
}

func validConfig(serverURL string) Config {
	return Config{
		ServerURL:    serverURL,
		ClientID:     "id",
		ClientSecret: "secret",
	}
}

func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name:    "missing serverURL",
			cfg:     Config{ClientID: "x", ClientSecret: "y"},
			wantErr: "ServerURL is required",
		},
		{
			name:    "missing clientID",
			cfg:     Config{ServerURL: "https://example", ClientSecret: "y"},
			wantErr: "ClientID and ClientSecret",
		},
		{
			name:    "missing clientSecret",
			cfg:     Config{ServerURL: "https://example", ClientID: "x"},
			wantErr: "ClientID and ClientSecret",
		},
		{
			name: "valid",
			cfg:  Config{ServerURL: "https://example", ClientID: "x", ClientSecret: "y"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate: want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestPingAcquiresToken(t *testing.T) {
	fp := newFakePolaris(t)
	c, err := NewClient(validConfig(fp.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := fp.tokenCalls.Load(); got != 1 {
		t.Errorf("token endpoint hit %d times, want 1", got)
	}
}

func TestTokenIsCachedAcrossCalls(t *testing.T) {
	fp := newFakePolaris(t)
	c, err := NewClient(validConfig(fp.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	for i := range 5 {
		if _, err := c.Management.ListCatalogs(context.Background()); err != nil {
			t.Fatalf("ListCatalogs[%d]: %v", i, err)
		}
	}
	if got := fp.tokenCalls.Load(); got != 1 {
		t.Errorf("token endpoint hit %d times across 5 mgmt calls, want 1 (token should be cached)", got)
	}
	if got := fp.mgmtCalls.Load(); got != 5 {
		t.Errorf("mgmt endpoint hit %d times, want 5", got)
	}
}

func TestTokenRefreshesNearExpiry(t *testing.T) {
	fp := newFakePolaris(t)
	c, err := NewClient(validConfig(fp.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping #1: %v", err)
	}
	// Force the cached token to look near-expired. With tokenLeadTime = 30s
	// any expiresAt within that window should trigger a refresh.
	c.tokenSource.mu.Lock()
	c.tokenSource.expiresAt = time.Now().Add(5 * time.Second)
	c.tokenSource.mu.Unlock()

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping #2: %v", err)
	}
	if got := fp.tokenCalls.Load(); got != 2 {
		t.Errorf("token endpoint hit %d times, want 2 (refresh expected)", got)
	}
}

func TestPingPropagatesAuthFailure(t *testing.T) {
	fp := newFakePolaris(t)
	fp.tokenStatus = http.StatusUnauthorized
	fp.tokenBody = `{"error":"invalid_client"}`

	c, err := NewClient(validConfig(fp.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping: expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Ping: error is %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("APIError.StatusCode = %d, want 401", apiErr.StatusCode)
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized(err) = false, want true")
	}
}

func TestErrorClassifiers(t *testing.T) {
	cases := []struct {
		status              int
		notFound, conflict  bool
		unauthorized, retry bool
	}{
		{http.StatusNotFound, true, false, false, false},
		{http.StatusConflict, false, true, false, false},
		{http.StatusUnauthorized, false, false, true, false},
		{http.StatusTooManyRequests, false, false, false, true},
		{http.StatusBadGateway, false, false, false, true},
		{http.StatusServiceUnavailable, false, false, false, true},
		{http.StatusGatewayTimeout, false, false, false, true},
		{http.StatusInternalServerError, false, false, false, false},
		{http.StatusBadRequest, false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			err := &APIError{StatusCode: tc.status, Op: "test", Body: "x"}
			if IsNotFound(err) != tc.notFound {
				t.Errorf("IsNotFound = %v, want %v", IsNotFound(err), tc.notFound)
			}
			if IsConflict(err) != tc.conflict {
				t.Errorf("IsConflict = %v, want %v", IsConflict(err), tc.conflict)
			}
			if IsUnauthorized(err) != tc.unauthorized {
				t.Errorf("IsUnauthorized = %v, want %v", IsUnauthorized(err), tc.unauthorized)
			}
			if IsRetryable(err) != tc.retry {
				t.Errorf("IsRetryable = %v, want %v", IsRetryable(err), tc.retry)
			}
		})
	}
}

func TestNonAPIErrorIsRetryable(t *testing.T) {
	// Network failures (non-*APIError) should be treated as retryable so
	// reconcilers can back off and try again on transient outages.
	if !IsRetryable(errors.New("dial tcp: connection refused")) {
		t.Error("IsRetryable on network error = false, want true")
	}
	if IsRetryable(nil) {
		t.Error("IsRetryable(nil) = true, want false")
	}
}

func TestInjectBearerSkipsTokenEndpoint(t *testing.T) {
	// The token endpoint itself must not carry a bearer header — otherwise
	// the token fetch recurses forever.
	fp := newFakePolaris(t)
	c, err := NewClient(validConfig(fp.srv.URL))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, fp.srv.URL+DefaultTokenPath, nil)
	if err := c.tokenSource.injectBearer(context.Background(), req); err != nil {
		t.Fatalf("injectBearer: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization header set on token endpoint: %q", got)
	}
	if got := fp.tokenCalls.Load(); got != 0 {
		t.Errorf("injectBearer triggered a token fetch (calls=%d) — must skip", got)
	}
}

func TestBuildTransportCABundleInvalid(t *testing.T) {
	cfg := validConfig("https://example")
	cfg.CABundle = []byte("not a cert")
	_, err := NewClient(cfg)
	if err == nil || !strings.Contains(err.Error(), "CABundle") {
		t.Fatalf("NewClient: want CABundle error, got %v", err)
	}
}

func TestJoinURL(t *testing.T) {
	cases := []struct {
		base, segment, want string
	}{
		{"https://h", "/a/b", "https://h/a/b"},
		{"https://h/", "/a/b", "https://h/a/b"},
		{"https://h/prefix", "x", "https://h/prefix/x"},
		{"https://h/prefix/", "x", "https://h/prefix/x"},
		{"https://h", "", "https://h"},
	}
	for _, tc := range cases {
		got, err := joinURL(tc.base, tc.segment)
		if err != nil {
			t.Errorf("joinURL(%q, %q): %v", tc.base, tc.segment, err)
			continue
		}
		if got != tc.want {
			t.Errorf("joinURL(%q, %q) = %q, want %q", tc.base, tc.segment, got, tc.want)
		}
	}
}
