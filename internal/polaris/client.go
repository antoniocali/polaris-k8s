/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package polaris wraps the auto-generated Apache Polaris management and
// catalog clients with the cross-cutting concerns the operator needs:
// OAuth client-credentials authentication, TLS configuration (CA bundles,
// insecureSkipVerify), and structured error mapping.
//
// The generated low-level clients live under ./management and ./catalog and
// are derived from the vendored OpenAPI specs in openapi/. Reconcilers should
// hold a *Client, not the generated sub-clients directly, so authentication
// and retries stay centralised.
package polaris

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/antoniocali/polaris-k8s/internal/polaris/catalog"
	"github.com/antoniocali/polaris-k8s/internal/polaris/management"
)

const (
	// DefaultTokenPath is the standard Polaris OAuth client-credentials endpoint.
	DefaultTokenPath = "/api/catalog/v1/oauth/tokens"

	// DefaultScope grants the operator the full management surface needed to
	// reconcile catalogs, principals, roles, and grants.
	DefaultScope = "PRINCIPAL_ROLE:ALL"

	// DefaultHTTPTimeout caps any individual HTTP request to the server.
	DefaultHTTPTimeout = 30 * time.Second

	// managementBasePath is the URL prefix for the Polaris management plane.
	// Matches the server URL declared in openapi/polaris-management-service.yaml.
	managementBasePath = "/api/management/v1"

	// catalogBasePath is the URL prefix for the Polaris/Iceberg catalog plane.
	// The catalog spec's operation paths already start with /v1 (Iceberg REST
	// convention), so the base here is just /api/catalog — without /v1 — to
	// avoid doubling.
	catalogBasePath = "/api/catalog"
)

// Config holds everything needed to talk to a Polaris instance. Reconcilers
// build one of these from a PolarisConnection CR plus the referenced Secret.
type Config struct {
	// ServerURL is the base URL of the Polaris server, e.g. https://polaris.internal.
	ServerURL string

	// TokenPath is the OAuth token endpoint path appended to ServerURL.
	// Defaults to DefaultTokenPath.
	TokenPath string

	// Scope is the OAuth scope requested when minting tokens. Defaults to
	// DefaultScope.
	Scope string

	// ClientID / ClientSecret are the OAuth client credentials, read from the
	// Secret named on the PolarisConnection.
	ClientID     string
	ClientSecret string

	// CABundle is a PEM-encoded CA certificate bundle used to validate the
	// Polaris server certificate. Optional.
	CABundle []byte

	// InsecureSkipVerify disables server certificate validation. Strongly
	// discouraged outside of local dev.
	InsecureSkipVerify bool

	// HTTPTimeout caps the duration of any single request. Defaults to
	// DefaultHTTPTimeout.
	HTTPTimeout time.Duration
}

// Validate reports a problem before we ever try to dial the server.
func (c Config) Validate() error {
	if c.ServerURL == "" {
		return errors.New("polaris: ServerURL is required")
	}
	if _, err := url.Parse(c.ServerURL); err != nil {
		return fmt.Errorf("polaris: ServerURL is not a valid URL: %w", err)
	}
	if c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("polaris: ClientID and ClientSecret are required")
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.TokenPath == "" {
		c.TokenPath = DefaultTokenPath
	}
	if c.Scope == "" {
		c.Scope = DefaultScope
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = DefaultHTTPTimeout
	}
	return c
}

// Client is the operator-facing Polaris client. It exposes the management
// and catalog plane sub-clients with auth and TLS already wired in. The
// zero value is not usable — always construct one via NewClient.
//
// The sub-clients are the typed-response variants (`*ClientWithResponses`)
// so reconcilers get back parsed bodies with status code helpers instead of
// raw `*http.Response`. The untyped variants are still reachable via
// `.ClientInterface` on each generated package if a caller needs them.
type Client struct {
	// Management talks to /api/management/v1 (catalogs, principals, roles, grants).
	Management *management.ClientWithResponses

	// Catalog talks to /api/catalog/v1 (namespaces, tables, views, policies).
	Catalog *catalog.ClientWithResponses

	// HTTP is the underlying client used for both planes — exposed so tests can
	// observe traffic without poking through the generated clients.
	HTTP *http.Client

	cfg         Config
	tokenSource *tokenSource
}

// NewClient builds a Polaris client from the given config. The OAuth token
// is acquired lazily on the first authenticated request.
func NewClient(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()

	transport, err := buildTransport(cfg)
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   cfg.HTTPTimeout,
	}

	ts := newTokenSource(cfg, httpClient)

	mgmtURL, err := joinURL(cfg.ServerURL, managementBasePath)
	if err != nil {
		return nil, fmt.Errorf("polaris: build management URL: %w", err)
	}
	catURL, err := joinURL(cfg.ServerURL, catalogBasePath)
	if err != nil {
		return nil, fmt.Errorf("polaris: build catalog URL: %w", err)
	}

	mgmt, err := management.NewClientWithResponses(
		mgmtURL,
		management.WithHTTPClient(httpClient),
		management.WithRequestEditorFn(ts.injectBearer),
	)
	if err != nil {
		return nil, fmt.Errorf("polaris: build management client: %w", err)
	}

	cat, err := catalog.NewClientWithResponses(
		catURL,
		catalog.WithHTTPClient(httpClient),
		catalog.WithRequestEditorFn(ts.injectBearer),
	)
	if err != nil {
		return nil, fmt.Errorf("polaris: build catalog client: %w", err)
	}

	return &Client{
		Management:  mgmt,
		Catalog:     cat,
		HTTP:        httpClient,
		cfg:         cfg,
		tokenSource: ts,
	}, nil
}

// Ping issues a token fetch to confirm credentials are valid. Reconcilers
// call this to set the AuthValid condition on PolarisConnection.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.tokenSource.token(ctx)
	return err
}

// buildTransport assembles an http.Transport with TLS configured from the
// config. We start from http.DefaultTransport.Clone() so behaviour matches
// the stdlib for everything we don't override.
func buildTransport(cfg Config) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("polaris: http.DefaultTransport is not *http.Transport")
	}
	t := base.Clone()

	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // opt-in via config
	}

	if len(cfg.CABundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(cfg.CABundle) {
			return nil, errors.New("polaris: CABundle did not contain any valid certificates")
		}
		tlsCfg.RootCAs = pool
	}

	t.TLSClientConfig = tlsCfg
	return t, nil
}
