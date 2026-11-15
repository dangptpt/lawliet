// Package meta implements flat, typed record metadata (DESIGN.md §3.4).
//
// Metadata arrives as a flat JSON object. Values are typed on parse:
// string, int64, float64, bool or []string. Nested objects, null and
// arrays of non-strings are rejected.
//
// A Map is immutable once stored: updates replace the whole Map, so it can
// be shared between readers without copying.
package meta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Kind is the type of a metadata value.
type Kind uint8

const (
	String Kind = iota
	Int
	Float
	Bool
	StringList
)

func (k Kind) String() string {
	switch k {
	case String:
		return "string"
	case Int:
		return "int"
	case Float:
		return "float"
	case Bool:
		return "bool"
	case StringList:
		return "string list"
	}
	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// Value is one typed metadata value. Only the field matching Kind is set.
type Value struct {
	Kind Kind
	S    string
	I    int64
	F    float64
	B    bool
	L    []string
}

// Number returns the value as float64 for numeric comparison.
func (v Value) Number() (float64, bool) {
	switch v.Kind {
	case Int:
		return float64(v.I), true
	case Float:
		return v.F, true
	}
	return 0, false
}

// Map is a record's metadata: field name -> value. A nil Map means "no metadata".
type Map map[string]Value

// Error reports invalid metadata input.
type Error struct{ Msg string }

func (e *Error) Error() string { return "invalid metadata: " + e.Msg }

func errorf(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// ParseJSON parses a flat JSON object. An empty object returns a nil Map.
func ParseJSON(data []byte) (Map, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) || bytes.HasPrefix(bytes.TrimSpace(data), []byte("null")) {
			return nil, errorf("must be a JSON object")
		}
		return nil, errorf("%v", err)
	}
	if raw == nil {
		return nil, errorf("must be a JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errorf("unexpected data after JSON object")
	}
	if len(raw) == 0 {
		return nil, nil
	}

	m := make(Map, len(raw))
	for k, v := range raw {
		if k == "" {
			return nil, errorf("field name must not be empty")
		}
		val, err := convert(k, v)
		if err != nil {
			return nil, err
		}
		m[k] = val
	}
	return m, nil
}

func convert(key string, v any) (Value, error) {
	switch x := v.(type) {
	case string:
		return Value{Kind: String, S: x}, nil
	case bool:
		return Value{Kind: Bool, B: x}, nil
	case json.Number:
		return parseNumber(key, string(x))
	case []any:
		list := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return Value{}, errorf("field '%s': arrays may only contain strings", key)
			}
			list[i] = s
		}
		return Value{Kind: StringList, L: list}, nil
	case nil:
		return Value{}, errorf("field '%s': null is not allowed", key)
	case map[string]any:
		return Value{}, errorf("field '%s': nested object not allowed", key)
	}
	return Value{}, errorf("field '%s': unsupported type %T", key, v)
}

// parseNumber returns Int for integer literals that fit in int64, Float otherwise.
func parseNumber(key, s string) (Value, error) {
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return Value{Kind: Int, I: i}, nil
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return Value{}, errorf("field '%s': number out of range", key)
	}
	return Value{Kind: Float, F: f}, nil
}

// AppendJSON appends m as a JSON object with sorted keys.
// Floats always keep a decimal point or exponent, so they parse back as Float.
func (m Map) AppendJSON(buf []byte) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	buf = append(buf, '{')
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendString(buf, k)
		buf = append(buf, ':')
		v := m[k]
		switch v.Kind {
		case String:
			buf = appendString(buf, v.S)
		case Int:
			buf = strconv.AppendInt(buf, v.I, 10)
		case Float:
			start := len(buf)
			buf = strconv.AppendFloat(buf, v.F, 'g', -1, 64)
			if !bytes.ContainsAny(buf[start:], ".eE") {
				buf = append(buf, ".0"...)
			}
		case Bool:
			buf = strconv.AppendBool(buf, v.B)
		case StringList:
			buf = append(buf, '[')
			for j, s := range v.L {
				if j > 0 {
					buf = append(buf, ',')
				}
				buf = appendString(buf, s)
			}
			buf = append(buf, ']')
		}
	}
	return append(buf, '}')
}

// JSON returns m encoded as a JSON object with sorted keys.
func (m Map) JSON() []byte { return m.AppendJSON(nil) }

func appendString(buf []byte, s string) []byte {
	b, _ := json.Marshal(s) // cannot fail for a string
	return append(buf, b...)
}
