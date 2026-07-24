// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Unit tests for the runtime's lenient ISO-8601 timestamp.

package platform

import (
	"encoding/json"
	"testing"
)

func TestParseTimeAcceptsTheLayoutsTheAPIEmits(t *testing.T) {
	accepted := []string{
		"2024-01-01T00:00:00Z",
		"2024-01-01T00:00:00.123Z",
		"2024-01-01T00:00:00+02:00",
		"2024-01-01T00:00:00",
		"2024-01-01 00:00:00",
		"2024-01-01",
	}
	for _, value := range accepted {
		if _, err := ParseTime(value); err != nil {
			t.Fatalf("ParseTime(%q): %v", value, err)
		}
	}
	if _, err := ParseTime("not-a-date"); err == nil {
		t.Fatal("expected ParseTime to reject a non-timestamp")
	}
}

func TestTimeDecodesInsideModels(t *testing.T) {
	type stamped struct {
		At Time `json:"at"`
	}
	var decoded stamped
	if err := json.Unmarshal([]byte(`{"at":"2024-06-01T12:30:00.500Z"}`), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.At.Year() != 2024 || decoded.At.Month() != 6 {
		t.Fatalf("expected June 2024, got %s", decoded.At)
	}
}

func TestTimeTreatsNullAndEmptyAsZero(t *testing.T) {
	type stamped struct {
		At Time `json:"at"`
	}
	for _, body := range []string{`{"at":null}`, `{"at":""}`, `{}`} {
		var decoded stamped
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if !decoded.At.IsZero() {
			t.Fatalf("expected the zero time for %s, got %s", body, decoded.At)
		}
	}
}

func TestTimeEncodesAsRFC3339(t *testing.T) {
	type stamped struct {
		At Time `json:"at"`
	}
	data, err := json.Marshal(stamped{At: MustParseTime("2024-06-01T12:30:00Z")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"at":"2024-06-01T12:30:00Z"}` {
		t.Fatalf("unexpected encoding: %s", data)
	}
}
