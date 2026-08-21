// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestUserProfileSendsExplicitClearFlags(t *testing.T) {
	var method string
	var path string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(WithBaseURL(server.URL))
	_, err := client.V1.Users.Profile(context.Background(), "me", UserProfileInput{
		ClearFullName:       Ptr(true),
		ClearProfilePicture: Ptr(true),
	})
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}

	if method != http.MethodPut || path != "/api/v1/users/me/profile" {
		t.Fatalf("request = %s %s", method, path)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	want := map[string]any{
		"clear_full_name":       true,
		"clear_profile_picture": true,
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("request body = %#v, want %#v", decoded, want)
	}
}
