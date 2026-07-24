// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: lenient ISO-8601 timestamp for the generated Platform SDK.
// This file is hand-maintained, not generated.

package platform

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Layouts accepted when decoding a timestamp, most specific first. The
// platform emits RFC 3339, but mocks and older endpoints have shipped
// zone-less and date-only values; a strict time.Time field would fail the
// whole response over one of them.
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Time is an ISO-8601 timestamp. It embeds time.Time, so the full standard
// library API is available on it.
type Time struct {
	time.Time
}

// ParseTime parses an ISO-8601 timestamp.
func ParseTime(value string) (Time, error) {
	for _, layout := range timeLayouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return Time{Time: parsed}, nil
		}
	}
	return Time{}, fmt.Errorf("platform: %q is not an ISO-8601 timestamp", value)
}

// MustParseTime parses an ISO-8601 timestamp and panics if it cannot.
// Intended for literals in tests and examples.
func MustParseTime(value string) Time {
	parsed, err := ParseTime(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

// MarshalJSON implements json.Marshaler, emitting RFC 3339.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Format(time.RFC3339Nano))
}

// UnmarshalJSON implements json.Unmarshaler. A JSON null or an empty string
// decodes to the zero Time rather than an error.
func (t *Time) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == `""` {
		t.Time = time.Time{}
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("platform: timestamp must be a JSON string: %w", err)
	}
	parsed, err := ParseTime(value)
	if err != nil {
		return err
	}
	t.Time = parsed.Time
	return nil
}

// queryString is the wire form of a timestamp used as a query parameter.
func (t Time) queryString() string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
