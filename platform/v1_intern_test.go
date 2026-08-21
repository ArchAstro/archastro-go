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

func TestInternPluginInvokeSendsInstallationAndOperation(t *testing.T) {
	var method string
	var path string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(WithBaseURL(server.URL))
	result, err := client.Intern().Plugins.Invoke.Create(context.Background(), InvokeCreateInput{
		Installation: &InvokeCreateInputInstallation{
			Binding:         "me",
			Config:          map[string]JSONValue{},
			Plugin:          "me",
			ProtocolVersion: 1,
		},
		Operation: "update",
		Input: map[string]JSONValue{
			"name": JSONOf("Updated Intern Viewer"),
		},
	})
	if err != nil {
		t.Fatalf("invoke Intern plugin: %v", err)
	}

	if method != http.MethodPost || path != "/api/v1/intern/plugins/invoke" {
		t.Fatalf("request = %s %s", method, path)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request body %q: %v", body, err)
	}
	want := map[string]any{
		"installation": map[string]any{
			"binding":          "me",
			"config":           map[string]any{},
			"plugin":           "me",
			"protocol_version": float64(1),
		},
		"operation": "update",
		"input":     map[string]any{"name": "Updated Intern Viewer"},
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("request body = %#v, want %#v", decoded, want)
	}
	if !result.Get("ok").BoolValue() {
		t.Fatalf("response = %s", result.String())
	}
}
