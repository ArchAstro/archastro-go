// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for the hand-maintained runtime's JSON value type. The contract
// tests cover the generated surface; these cover runtime behavior that needs
// no server.

package platform

import (
	"encoding/json"
	"testing"
)

func TestJSONValueAccessorsReadNestedPayloads(t *testing.T) {
	value := JSONOf(map[string]any{
		"name":   "Acme",
		"count":  3,
		"ratio":  1.5,
		"active": true,
		"tags":   []any{"a", "b"},
		"meta":   map[string]any{},
	})

	if got := value.Get("name").StringValue(); got != "Acme" {
		t.Fatalf("name: expected Acme, got %q", got)
	}
	if got := value.Get("count").IntValue(); got != 3 {
		t.Fatalf("count: expected 3, got %d", got)
	}
	if got := value.Get("ratio").FloatValue(); got != 1.5 {
		t.Fatalf("ratio: expected 1.5, got %v", got)
	}
	if !value.Get("active").BoolValue() {
		t.Fatal("active: expected true")
	}
	if got := value.Get("tags").Index(1).StringValue(); got != "b" {
		t.Fatalf("tags[1]: expected b, got %q", got)
	}
	if value.Get("meta").Object() == nil {
		t.Fatal("meta: expected an empty object, got a non-object")
	}
	if !value.Get("missing").IsNull() {
		t.Fatal("missing: expected null for an absent key")
	}
}

func TestJSONValueAccessorsAreTypeSafeOnMismatch(t *testing.T) {
	value := JSONOf("plain")

	// Every accessor has to survive being asked for the wrong type: the
	// generated code chains them without checking, on payloads the server
	// controls.
	if got := value.IntValue(); got != 0 {
		t.Fatalf("IntValue on a string: expected 0, got %d", got)
	}
	if got := value.Get("nope").StringValue(); got != "" {
		t.Fatalf("Get on a string: expected \"\", got %q", got)
	}
	if got := value.Index(0).StringValue(); got != "" {
		t.Fatalf("Index on a string: expected \"\", got %q", got)
	}
	if value.Array() != nil {
		t.Fatal("Array on a string: expected nil")
	}
	if value.Object() != nil {
		t.Fatal("Object on a string: expected nil")
	}
}

func TestJSONValueRoundTripsThroughEncodingJSON(t *testing.T) {
	original := JSONOf(map[string]any{
		"nested": map[string]any{"list": []any{1, 2.5, true, "x", nil}},
	})

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded JSONValue
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !decoded.Equal(original) {
		t.Fatalf("round trip changed the value: %s vs %s", decoded, original)
	}
}

func TestJSONValuePreservesLargeIntegers(t *testing.T) {
	// A float64 round trip would quietly lose the low bits here; UseNumber
	// is what keeps IDs exact.
	var value JSONValue
	if err := value.UnmarshalJSON([]byte(`{"id":9007199254740993}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := value.String(); got != `{"id":9007199254740993}` {
		t.Fatalf("expected the integer to survive, got %s", got)
	}
}

func TestJSONValueDecodeIntoTypedStruct(t *testing.T) {
	type point struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	var decoded point
	if err := JSONOf(map[string]any{"x": 1, "y": 2}).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.X != 1 || decoded.Y != 2 {
		t.Fatalf("expected {1 2}, got %+v", decoded)
	}
}

func TestJSONValueQueryStringKeepsBareStrings(t *testing.T) {
	if got := JSONOf("plain").queryString(); got != "plain" {
		t.Fatalf("expected plain, got %q", got)
	}
	if got := JSONOf(map[string]any{"a": 1}).queryString(); got != `{"a":1}` {
		t.Fatalf(`expected {"a":1}, got %q`, got)
	}
	if got := JSONOf(true).queryString(); got != "true" {
		t.Fatalf("expected true, got %q", got)
	}
}

func TestQueryParamRendersEveryParameterType(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"string", "raw value", "raw value"},
		{"int", 42, "42"},
		{"bool", false, "false"},
		{"float", 1.25, "1.25"},
		{"time", MustParseTime("2024-01-01T00:00:00Z"), "2024-01-01T00:00:00Z"},
		{"json", JSONOf([]any{1, 2}), "[1,2]"},
		{"slice", []string{"a", "b"}, `["a","b"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := queryParam(tc.value); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestPtrRoundTrip(t *testing.T) {
	if got := *Ptr("x"); got != "x" {
		t.Fatalf("expected x, got %q", got)
	}
	if got := *Ptr(7); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}
