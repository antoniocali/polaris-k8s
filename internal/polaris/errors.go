/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package polaris

import (
	"errors"
	"fmt"
	"net/http"
)

// APIError represents a non-2xx response from the Polaris server. It is the
// canonical error type reconcilers compare against to decide whether to
// retry, surface the error to the user via a status condition, or fail
// hard.
type APIError struct {
	// StatusCode is the HTTP status returned by Polaris.
	StatusCode int

	// Op is a short identifier for the operation that failed
	// (e.g. "auth/token", "catalogs/create"). Used purely for logging.
	Op string

	// Body is the raw response body, preserved verbatim so reconcilers can
	// surface it in status conditions when useful.
	Body string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("polaris %s: HTTP %d: %s", e.Op, e.StatusCode, e.Body)
}

// IsNotFound reports whether err is an APIError with a 404 status.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusNotFound
}

// IsConflict reports whether err is an APIError with a 409 status —
// useful for create-if-not-exists flows.
func IsConflict(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusConflict
}

// IsUnauthorized reports whether err is an APIError with a 401 status,
// typically indicating bad/expired credentials.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusUnauthorized
}

// IsRetryable reports whether err is worth retrying after a back-off.
// True for transient HTTP states (429, 502, 503, 504) and for non-API
// errors (network failures during the request).
func IsRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		// Network / context errors are non-APIError; treat as retryable so
		// the caller can decide based on context.
		return err != nil
	}
	switch apiErr.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}
