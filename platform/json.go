// Copyright (c) 2026 ArchAstro Inc. Licensed under the MIT License.
// See LICENSE for details.

// Runtime: dynamically typed JSON value for the generated Platform SDK.
// This file is hand-maintained, not generated. Go counterpart of the Swift
// SDK's JSONValue enum and the Python SDK's untyped dict payloads.

package platform

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// JSONValue is a decoded JSON value of unknown shape: the type generated
// methods return for untyped responses, and the type channel payloads and
// error bodies arrive as.
//
// The zero value is JSON null. Accessors never panic — a type mismatch or a
// missing key yields the zero value of what you asked for, so chains like
// body.Get("error").Get("code").StringValue() are safe on any payload.
type JSONValue struct {
	value any
}

// JSONOf converts any Go value into a JSONValue by round-tripping it
// through encoding/json. Values that cannot be marshaled (channels, funcs,
// cycles) become JSON null.
func JSONOf(v any) JSONValue {
	if existing, ok := v.(JSONValue); ok {
		return existing
	}
	data, err := json.Marshal(v)
	if err != nil {
		return JSONValue{}
	}
	var out JSONValue
	if err := json.Unmarshal(data, &out); err != nil {
		return JSONValue{}
	}
	return out
}

// MarshalJSON implements json.Marshaler.
func (j JSONValue) MarshalJSON() ([]byte, error) {
	if j.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(j.value)
}

// UnmarshalJSON implements json.Unmarshaler. Numbers are kept as
// json.Number so large integers survive the round trip intact.
func (j *JSONValue) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	j.value = value
	return nil
}

// Raw returns the underlying Go value (map[string]any, []any, string,
// json.Number, bool, or nil).
func (j JSONValue) Raw() any { return j.value }

// IsNull reports whether the value is JSON null (or absent).
func (j JSONValue) IsNull() bool { return j.value == nil }

// Get indexes an object. A non-object, or a missing key, yields null.
func (j JSONValue) Get(key string) JSONValue {
	object, ok := j.value.(map[string]any)
	if !ok {
		return JSONValue{}
	}
	value, ok := object[key]
	if !ok {
		return JSONValue{}
	}
	return JSONValue{value: value}
}

// Index indexes an array. A non-array, or an out-of-range index, yields null.
func (j JSONValue) Index(i int) JSONValue {
	array, ok := j.value.([]any)
	if !ok || i < 0 || i >= len(array) {
		return JSONValue{}
	}
	return JSONValue{value: array[i]}
}

// StringValue returns the string payload, or "" for any other type.
func (j JSONValue) StringValue() string {
	if s, ok := j.value.(string); ok {
		return s
	}
	return ""
}

// IntValue returns the integer payload, or 0 for any other type.
func (j JSONValue) IntValue() int {
	switch value := j.value.(type) {
	case json.Number:
		if n, err := value.Int64(); err == nil {
			return int(n)
		}
		if f, err := value.Float64(); err == nil {
			return int(f)
		}
	case float64:
		return int(value)
	case int:
		return value
	}
	return 0
}

// FloatValue returns the number payload, or 0 for any other type.
func (j JSONValue) FloatValue() float64 {
	switch value := j.value.(type) {
	case json.Number:
		if f, err := value.Float64(); err == nil {
			return f
		}
	case float64:
		return value
	case int:
		return float64(value)
	}
	return 0
}

// BoolValue returns the boolean payload, or false for any other type.
func (j JSONValue) BoolValue() bool {
	if b, ok := j.value.(bool); ok {
		return b
	}
	return false
}

// Array returns the array payload, or nil for any other type.
func (j JSONValue) Array() []JSONValue {
	array, ok := j.value.([]any)
	if !ok {
		return nil
	}
	out := make([]JSONValue, len(array))
	for i, item := range array {
		out[i] = JSONValue{value: item}
	}
	return out
}

// Object returns the object payload, or nil for any other type.
func (j JSONValue) Object() map[string]JSONValue {
	object, ok := j.value.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]JSONValue, len(object))
	for key, item := range object {
		out[key] = JSONValue{value: item}
	}
	return out
}

// Decode re-decodes this value into a typed destination.
func (j JSONValue) Decode(target any) error {
	data, err := j.MarshalJSON()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// String renders the value as compact JSON.
func (j JSONValue) String() string {
	data, err := j.MarshalJSON()
	if err != nil {
		return ""
	}
	return string(data)
}

// Equal reports whether two values carry the same JSON. Object keys are
// order-insensitive because encoding/json sorts map keys when marshaling.
func (j JSONValue) Equal(other JSONValue) bool {
	left, err := j.MarshalJSON()
	if err != nil {
		return false
	}
	right, err := other.MarshalJSON()
	if err != nil {
		return false
	}
	return bytes.Equal(left, right)
}

// queryString is the wire form of a value used as a query parameter: bare
// strings stay unquoted, everything else becomes compact JSON.
func (j JSONValue) queryString() string {
	if s, ok := j.value.(string); ok {
		return s
	}
	return j.String()
}

// Ptr returns a pointer to v. Generated optional fields and parameters are
// pointers so "unset" is distinguishable from "zero"; this is the shorthand
// for filling them in from a literal.
func Ptr[T any](v T) *T { return &v }

// queryParam renders a value in the string form a query parameter takes on
// the wire. Generated resource methods call it for every non-string
// parameter.
func queryParam(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		return strconv.FormatBool(value)
	case int:
		return strconv.Itoa(value)
	case int32:
		return strconv.FormatInt(int64(value), 10)
	case int64:
		return strconv.FormatInt(value, 10)
	case float32:
		return strconv.FormatFloat(float64(value), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case Time:
		return value.queryString()
	case JSONValue:
		return value.queryString()
	default:
		return JSONOf(v).queryString()
	}
}
