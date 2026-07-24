// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: error types for the generated Platform SDK.
// This file is hand-maintained, not generated.

package platform

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by client construction and the socket helpers.
var (
	// ErrMissingAccessToken is returned when opening a socket without an
	// access token to authenticate the connection.
	ErrMissingAccessToken = errors.New("platform: an access token is required")
	// ErrLoginFailed is returned when a credential login succeeds at the
	// HTTP layer but yields no access token.
	ErrLoginFailed = errors.New("platform: login did not return an access token")
	// ErrRefreshFailed is returned when automatic token refresh cannot
	// produce a new access token.
	ErrRefreshFailed = errors.New("platform: token refresh did not return an access token")
	// ErrRefreshOnly guards the dedicated refresh client: it may only call
	// the auth endpoints, so a refresh can never re-enter the 401 retry.
	ErrRefreshOnly = errors.New("platform: refresh-only client cannot call non-auth endpoints")
)

// APIError is a non-2xx response from the platform, with the structured
// error body parsed out. Match it with errors.As:
//
//	var apiErr *platform.APIError
//	if errors.As(err, &apiErr) && apiErr.Status == 404 { … }
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the machine-readable error code from the response body.
	Code string
	// Message is the human-readable message from the response body.
	Message string
	// Body is the full parsed response body, when there was one.
	Body JSONValue
}

func (e *APIError) Error() string {
	return fmt.Sprintf("platform: HTTP %d (%s): %s", e.Status, e.Code, e.Message)
}

// parseAPIError builds an APIError from a response body, tolerating the
// several error shapes the platform and its mocks emit:
// {"error": {"code", "message"}}, {"error": "string"}, and {"message"}.
func parseAPIError(data []byte, status int) *APIError {
	apiErr := &APIError{Status: status, Code: "unknown_error", Message: fmt.Sprintf("HTTP %d", status)}

	var body JSONValue
	if err := body.UnmarshalJSON(data); err != nil {
		return apiErr
	}
	apiErr.Body = body

	if nested := body.Get("error"); nested.Object() != nil {
		if code := nested.Get("code").StringValue(); code != "" {
			apiErr.Code = code
		} else if kind := nested.Get("type").StringValue(); kind != "" {
			apiErr.Code = kind
		}
		if message := nested.Get("message").StringValue(); message != "" {
			apiErr.Message = message
		}
		return apiErr
	}

	if text := body.Get("error").StringValue(); text != "" {
		apiErr.Code = text
		apiErr.Message = text
	}
	if message := body.Get("message").StringValue(); message != "" {
		apiErr.Message = message
	}
	return apiErr
}
