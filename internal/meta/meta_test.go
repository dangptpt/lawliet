package meta

import (
	"errors"
	"strings"
	"testing"
)

func TestParseJSONTypes(t *testing.T) {
	m, err := ParseJSON([]byte(`{"source":"hr.md","page":3,"score":0.5,"big":1e3,"huge":99999999999999999999,
		"public":true,"tags":["hr","policy"],"empty":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	checks := map[string]Value{
		"source": {Kind: String, S: "hr.md"},
		"page":   {Kind: Int, I: 3},
		"score":  {Kind: Float, F: 0.5},
		"big":    {Kind: Float, F: 1000},
		"huge":   {Kind: Float, F: 1e20},
		"public": {Kind: Bool, B: true},
	}
	for k, want := range checks {
		got := m[k]
		if got.Kind != want.Kind || got.S != want.S || got.I != want.I || got.F != want.F || got.B != want.B {
			t.Errorf("%s: got %+v want %+v", k, got, want)
		}
	}
	if tags := m["tags"]; tags.Kind != StringList || strings.Join(tags.L, ",") != "hr,policy" {
		t.Errorf("tags: %+v", tags)
	}
	if e := m["empty"]; e.Kind != StringList || len(e.L) != 0 {
		t.Errorf("empty: %+v", e)
	}
}

func TestParseJSONEmpty(t *testing.T) {
	m, err := ParseJSON([]byte(` {} `))
	if err != nil || m != nil {
		t.Fatalf("got %v, %v; want nil map", m, err)
	}
}

func TestParseJSONErrors(t *testing.T) {
	cases := map[string]string{
		`{"a":{"b":1}}`:   "nested object not allowed",
		`{"a":null}`:      "null is not allowed",
		`{"a":[1,2]}`:     "arrays may only contain strings",
		`{"a":["x",{}]}`:  "arrays may only contain strings",
		`[1,2]`:           "must be a JSON object",
		`"str"`:           "must be a JSON object",
		`null`:            "must be a JSON object",
		`{"a":1} {"b":2}`: "unexpected data after JSON object",
		`{"a":`:           "invalid metadata",
		`{"":1}`:          "field name must not be empty",
		`{"a":1e999}`:     "number out of range",
	}
	for in, want := range cases {
		_, err := ParseJSON([]byte(in))
		var me *Error
		if !errors.As(err, &me) || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseJSON(%s): got %v, want error containing %q", in, err, want)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	in := `{"z":"last","a":"say \"hi\"\n","f":3.0,"g":2.5,"i":-7,"b":false,"l":["x","y"]}`
	m, err := ParseJSON([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out := string(m.JSON())
	want := `{"a":"say \"hi\"\n","b":false,"f":3.0,"g":2.5,"i":-7,"l":["x","y"],"z":"last"}`
	if out != want {
		t.Fatalf("JSON:\n got  %s\n want %s", out, want)
	}
	// Re-parsing keeps kinds (3.0 stays Float, not Int).
	m2, err := ParseJSON([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if m2["f"].Kind != Float || m2["i"].Kind != Int {
		t.Fatalf("kinds changed after round trip: f=%v i=%v", m2["f"].Kind, m2["i"].Kind)
	}
}

func TestNumber(t *testing.T) {
	if f, ok := (Value{Kind: Int, I: 2}).Number(); !ok || f != 2 {
		t.Error("int")
	}
	if f, ok := (Value{Kind: Float, F: 2.5}).Number(); !ok || f != 2.5 {
		t.Error("float")
	}
	if _, ok := (Value{Kind: String, S: "2"}).Number(); ok {
		t.Error("string must not be numeric")
	}
}
