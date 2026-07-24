// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for structured API error parsing. The platform, its gateways,
// and the Prism mock each emit a slightly different error envelope; all of
// them have to land in the same APIError.

package platform

import (
	"errors"
	"testing"
)

func TestParseAPIErrorReadsStructuredErrorObjects(t *testing.T) {
	err := parseAPIError([]byte(`{"error":{"code":"not_found","message":"Agent missing"}}`), 404)
	if err.Status != 404 {
		t.Fatalf("expected status 404, got %d", err.Status)
	}
	if err.Code != "not_found" {
		t.Fatalf("expected code not_found, got %q", err.Code)
	}
	if err.Message != "Agent missing" {
		t.Fatalf("expected the server message, got %q", err.Message)
	}
}

func TestParseAPIErrorFallsBackToTypeWhenCodeMissing(t *testing.T) {
	err := parseAPIError([]byte(`{"error":{"type":"validation_error"}}`), 422)
	if err.Code != "validation_error" {
		t.Fatalf("expected code validation_error, got %q", err.Code)
	}
	if err.Message != "HTTP 422" {
		t.Fatalf("expected the status fallback message, got %q", err.Message)
	}
}

func TestParseAPIErrorHandlesStringErrorsAndBareMessages(t *testing.T) {
	stringError := parseAPIError([]byte(`{"error":"boom"}`), 400)
	if stringError.Code != "boom" || stringError.Message != "boom" {
		t.Fatalf("expected boom/boom, got %q/%q", stringError.Code, stringError.Message)
	}

	messageOnly := parseAPIError([]byte(`{"message":"nope"}`), 403)
	if messageOnly.Code != "unknown_error" {
		t.Fatalf("expected unknown_error, got %q", messageOnly.Code)
	}
	if messageOnly.Message != "nope" {
		t.Fatalf("expected nope, got %q", messageOnly.Message)
	}
}

func TestParseAPIErrorHandlesNonJSONBodies(t *testing.T) {
	err := parseAPIError([]byte("<html>oops</html>"), 500)
	if err.Code != "unknown_error" || err.Message != "HTTP 500" {
		t.Fatalf("expected the status fallback, got %q/%q", err.Code, err.Message)
	}
}

func TestAPIErrorMatchesWithErrorsAs(t *testing.T) {
	var err error = parseAPIError([]byte(`{"error":{"code":"gone"}}`), 410)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("expected errors.As to match *APIError")
	}
	if apiErr.Status != 410 {
		t.Fatalf("expected status 410, got %d", apiErr.Status)
	}
}
