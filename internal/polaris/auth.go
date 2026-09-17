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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenLeadTime is how early we refresh before expiry. Polaris tokens
// typically live for an hour; refreshing 30s early avoids clock-skew
// edge cases against the server.
const tokenLeadTime = 30 * time.Second

// tokenResponse is the OAuth 2.0 client-credentials response shape.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Scope       string `json:"scope,omitempty"`
}

// tokenSource caches an OAuth access token across requests and refreshes
// it when it nears expiry. Concurrent callers see at most one in-flight
// refresh.
type tokenSource struct {
	cfg  Config
	http *http.Client

	mu        sync.Mutex
	cached    string
	expiresAt time.Time
}

func newTokenSource(cfg Config, httpClient *http.Client) *tokenSource {
	return &tokenSource{cfg: cfg, http: httpClient}
}

// token returns a valid bearer token, fetching a new one if the cached
// one is missing or about to expire.
func (t *tokenSource) token(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cached != "" && time.Until(t.expiresAt) > tokenLeadTime {
		return t.cached, nil
	}

	resp, err := t.fetch(ctx)
	if err != nil {
		return "", err
	}
	t.cached = resp.AccessToken
	if resp.ExpiresIn > 0 {
		t.expiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	} else {
		// Server didn't tell us — assume one hour, the Polaris default.
		t.expiresAt = time.Now().Add(time.Hour)
	}
	return t.cached, nil
}

// injectBearer is the oapi-codegen RequestEditorFn we hand to both
// generated clients. It skips the token endpoint itself (which would
// recurse) and otherwise attaches `Authorization: Bearer <token>`.
func (t *tokenSource) injectBearer(ctx context.Context, req *http.Request) error {
	if strings.HasSuffix(req.URL.Path, t.cfg.TokenPath) {
		return nil
	}
	tok, err := t.token(ctx)
	if err != nil {
		return fmt.Errorf("polaris: acquire token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

// fetch performs the actual OAuth client-credentials exchange.
func (t *tokenSource) fetch(ctx context.Context) (*tokenResponse, error) {
	endpoint, err := joinURL(t.cfg.ServerURL, t.cfg.TokenPath)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", t.cfg.ClientID)
	form.Set("client_secret", t.cfg.ClientSecret)
	form.Set("scope", t.cfg.Scope)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polaris: post token endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("polaris: read token response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Op:         "auth/token",
			Body:       string(body),
		}
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("polaris: decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("polaris: token endpoint returned empty access_token (body: %s)", body)
	}
	return &tr, nil
}

// joinURL combines a base URL and a path segment. It tolerates the base
// already carrying a path (e.g. https://host/some/prefix) and the segment
// being absolute or relative.
func joinURL(base, segment string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if segment == "" {
		return u.String(), nil
	}
	if strings.HasPrefix(segment, "/") {
		u.Path = segment
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + "/" + segment
	}
	return u.String(), nil
}
