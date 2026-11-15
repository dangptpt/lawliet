package filter

import (
	"errors"
	"strings"
	"testing"

	"github.com/tranbaohieu/lawliet/internal/meta"
)

func mustMeta(t *testing.T, js string) meta.Map {
	t.Helper()
	m, err := meta.ParseJSON([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMatch(t *testing.T) {
	doc := mustMeta(t, `{"source":"hr.md","page":3,"score":0.75,"public":true,"tags":["hr","policy"]}`)

	cases := []struct {
		expr string
		want bool
	}{
		// equality
		{`source == "hr.md"`, true},
		{`source == 'hr.md'`, true},
		{`source != "hr.md"`, false},
		{`source != "it.md"`, true},
		{`public == true`, true},
		{`public == false`, false},
		{`page == 3`, true},
		{`page == 3.0`, true}, // int and float compare numerically
		{`score == 0.75`, true},

		// ordering
		{`page >= 3`, true},
		{`page > 3`, false},
		{`page < 3.5`, true},
		{`score <= 0.75`, true},
		{`score > -1`, true},
		{`page > 1e0`, true},

		// in / contains
		{`source in ["it.md", "hr.md"]`, true},
		{`source in ["it.md"]`, false},
		{`page in [1, 2, 3]`, true},
		{`tags contains "policy"`, true},
		{`tags contains "draft"`, false},

		// missing fields and type mismatches are false
		{`missing == 1`, false},
		{`missing != 1`, false},
		{`not missing == 1`, true},
		{`page == "3"`, false},
		{`page != "3"`, false},
		{`source > 1`, false},
		{`source contains "hr"`, false}, // contains needs a string list
		{`tags == "hr"`, false},
		{`tags in ["hr"]`, false},

		// boolean logic and precedence: not > and > or
		{`source == "hr.md" and page == 3`, true},
		{`source == "it.md" or page == 3`, true},
		{`source == "it.md" and page == 3 or public == true`, true},  // (F and T) or T
		{`source == "hr.md" or page == 9 and public == false`, true}, // T or (F and F)
		{`(source == "hr.md" or page == 9) and public == false`, false},
		{`not source == "hr.md" or page == 3`, true}, // (not T) or T
		{`not (source == "hr.md" or page == 3)`, false},
		{`not not public == true`, true},
		{`(source == "hr.md" or source in ["it.md", "ops.md"]) and page >= 2 and not tags contains "draft"`, true},

		// keywords are case-insensitive
		{`source == "hr.md" AND NOT page > 5 Or public == FALSE`, true},
	}
	for _, tc := range cases {
		f, err := Parse(tc.expr)
		if err != nil {
			t.Errorf("Parse(%s): %v", tc.expr, err)
			continue
		}
		if got := f.Match(doc); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.expr, got, tc.want)
		}
	}
}

func TestMatchNilMeta(t *testing.T) {
	f, _ := Parse(`not source == "x"`)
	if !f.Match(nil) {
		t.Error("not on missing field should match a record without metadata")
	}
	f, _ = Parse(`source == "x"`)
	if f.Match(nil) {
		t.Error("comparison on nil metadata must be false")
	}
}

func TestStringEscapes(t *testing.T) {
	doc := meta.Map{"q": {Kind: meta.String, S: `say "hi"` + "\n" + `it's`}}
	for _, expr := range []string{
		`q == "say \"hi\"\nit's"`,
		`q == 'say "hi"\nit\'s'`,
	} {
		f, err := Parse(expr)
		if err != nil {
			t.Fatalf("Parse(%s): %v", expr, err)
		}
		if !f.Match(doc) {
			t.Errorf("%s should match", expr)
		}
	}
}

func TestFieldNamesWithDotsAndUnicodeValues(t *testing.T) {
	doc := meta.Map{"user.id": {Kind: meta.Int, I: 7}, "title": {Kind: meta.String, S: "Chính sách nghỉ phép"}}
	f, err := Parse(`user.id == 7 and title == "Chính sách nghỉ phép"`)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match(doc) {
		t.Fatal("should match")
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		``:                          "empty filter",
		`   `:                       "empty filter",
		`page`:                      "expected operator after field 'page', got end of filter",
		`page = 3`:                  "unexpected '=' (use == or !=)",
		`page == `:                  "expected a value",
		`page > "3"`:                "operator > requires a number",
		`page < true`:               "operator < requires a number",
		`== 3`:                      "expected field name, got '=='",
		`(page == 3`:                "expected ')', got end of filter",
		`page == 3)`:                "unexpected ')'",
		`page == 3 page == 4`:       "unexpected 'page'",
		`page in 3`:                 "expected '[', got '3'",
		`page in [1 2]`:             "expected ',' or ']', got '2'",
		`page in []`:                "expected a value",
		`tags contains 3`:           "expected string, got '3'",
		`source == "unterminated`:   "unterminated string",
		`source == "bad \q escape"`: "unknown escape",
		`page == 3 and`:             "expected field name, got end of filter",
		`page # 3`:                  "unexpected character '#'",
		`page == -`:                 "invalid number",
	}
	for expr, want := range cases {
		_, err := Parse(expr)
		var fe *Error
		if !errors.As(err, &fe) || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q): got %v, want error containing %q", expr, err, want)
		}
	}
}

func TestErrorPosition(t *testing.T) {
	_, err := Parse(`page == 3 and source = "x"`)
	var fe *Error
	if !errors.As(err, &fe) || fe.Pos != 21 {
		t.Fatalf("got %v, want position 21", err)
	}
}

func TestLimits(t *testing.T) {
	deep := strings.Repeat("(", MaxDepth+2) + "a == 1" + strings.Repeat(")", MaxDepth+2)
	if _, err := Parse(deep); err == nil || !strings.Contains(err.Error(), "nesting deeper") {
		t.Errorf("deep nesting: %v", err)
	}
	notChain := strings.Repeat("not ", MaxDepth+2) + "a == 1"
	if _, err := Parse(notChain); err == nil || !strings.Contains(err.Error(), "nesting deeper") {
		t.Errorf("not chain: %v", err)
	}
	ok := strings.Repeat("(", 10) + "a == 1" + strings.Repeat(")", 10)
	if _, err := Parse(ok); err != nil {
		t.Errorf("moderate nesting should parse: %v", err)
	}

	items := make([]string, MaxInList+1)
	for i := range items {
		items[i] = "1"
	}
	if _, err := Parse("a in [" + strings.Join(items, ",") + "]"); err == nil {
		t.Error("too long in-list should fail")
	}
	if _, err := Parse(strings.Repeat(" ", MaxLength+1)); err == nil {
		t.Error("too long filter should fail")
	}
}

func BenchmarkMatch(b *testing.B) {
	f, _ := Parse(`(source == "hr.md" or source in ["it.md", "ops.md"]) and page >= 2 and not tags contains "draft"`)
	m := meta.Map{
		"source": {Kind: meta.String, S: "ops.md"},
		"page":   {Kind: meta.Int, I: 3},
		"tags":   {Kind: meta.StringList, L: []string{"a", "b"}},
	}
	for b.Loop() {
		f.Match(m)
	}
}
