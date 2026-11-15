package command

import (
	"strconv"
	"strings"
	"testing"

	"github.com/tranbaohieu/lawliet/internal/resp"
	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/server/servertest"
	"github.com/tranbaohieu/lawliet/internal/store"
)

func newClient(t *testing.T) *servertest.Client {
	t.Helper()
	addr := servertest.Start(t, func(s *server.Server) { Register(s, store.New()) })
	return servertest.Dial(t, addr)
}

func want(t *testing.T, v resp.Value, expected string) {
	t.Helper()
	if got := servertest.Describe(v); got != expected {
		t.Fatalf("got  %s\nwant %s", got, expected)
	}
}

func wantErr(t *testing.T, v resp.Value, prefix string) {
	t.Helper()
	if v.Type != '-' || !strings.HasPrefix(v.Str, prefix) {
		t.Fatalf("got %s, want error starting with %q", servertest.Describe(v), prefix)
	}
}

func TestCollectionLifecycle(t *testing.T) {
	c := newClient(t)
	want(t, c.Do("VCREATE", "docs", "DIM", 3), `+"OK"`)
	want(t, c.Do("vcreate", "faq", "dim", 2, "metric", "L2", "index", "FLAT"), `+"OK"`)
	wantErr(t, c.Do("VCREATE", "docs", "DIM", 3), "ERR collection already exists")
	want(t, c.Do("VLIST"), `*[$"docs" $"faq"]`)

	want(t, c.Do("VINFO", "docs"),
		`*[$"name" $"docs" $"dim" :3 $"metric" $"cosine" $"index" $"flat" $"count" :0 $"vector_bytes" :0]`)
	want(t, c.Do("VINFO", "faq").Map()["metric"], `$"l2"`)

	want(t, c.Do("VDROP", "faq"), ":1")
	want(t, c.Do("VDROP", "faq"), ":0")
	wantErr(t, c.Do("VINFO", "faq"), "ERR collection not found")
}

func TestVCreateErrors(t *testing.T) {
	c := newClient(t)
	cases := []struct {
		args []any
		err  string
	}{
		{[]any{"VCREATE", "x", "METRIC", "l2"}, "ERR DIM is required"},
		{[]any{"VCREATE", "x", "DIM", 0}, "ERR DIM must be an integer between 1 and 65536"},
		{[]any{"VCREATE", "x", "DIM", "abc"}, "ERR DIM must be an integer"},
		{[]any{"VCREATE", "x", "DIM", 3, "METRIC", "hamming"}, "ERR unknown metric 'hamming'"},
		{[]any{"VCREATE", "x", "DIM", 3, "INDEX", "hnsw"}, "ERR unsupported index 'hnsw'"},
		{[]any{"VCREATE", "x", "DIM", 3, "BOGUS", 1}, "ERR syntax error"},
		{[]any{"VCREATE", "x", "DIM", 3, "METRIC"}, "ERR syntax error"},
		{[]any{"VCREATE", "x", "DIM"}, "ERR wrong number of arguments"},
	}
	for _, tc := range cases {
		wantErr(t, c.Do(tc.args...), tc.err)
	}
	want(t, c.Do("VLIST"), "*[]")
}

func TestRecordCommands(t *testing.T) {
	c := newClient(t)
	c.Do("VCREATE", "v", "DIM", 2, "METRIC", "ip")

	want(t, c.Do("VADD", "v", "a", "VALUES", 2, "1", "2"), ":1")
	want(t, c.Do("VADD", "v", "a", "FP32", encodeFP32([]float32{3, 4})), ":0") // upsert
	want(t, c.Do("VADD", "v", "a", "VALUES", 2, "9", "9", "NX"), "nil")
	want(t, c.Do("VADD", "v", "b", "VALUES", 2, "9", "9", "XX"), "nil")
	want(t, c.Do("VADD", "v", "b", "VALUES", 2, "5", "6", "nx"), ":1")

	want(t, c.Do("VEXISTS", "v", "a"), ":1")
	want(t, c.Do("VEXISTS", "v", "zzz"), ":0")
	want(t, c.Do("VCOUNT", "v"), ":2")

	want(t, c.Do("VGET", "v", "a"), "*[nil nil]")
	want(t, c.Do("VGET", "v", "a", "WITHVEC"), `*[nil nil $`+strconv.Quote(string(encodeFP32([]float32{3, 4})))+`]`)
	want(t, c.Do("VGET", "v", "missing"), "nil")

	want(t, c.Do("VDEL", "v", "a", "missing"), ":1")
	want(t, c.Do("VCOUNT", "v"), ":1")
	want(t, c.Do("VEXISTS", "v", "a"), ":0")
}

func TestRecordErrors(t *testing.T) {
	c := newClient(t)
	c.Do("VCREATE", "v", "DIM", 3)
	cases := []struct {
		args []any
		err  string
	}{
		{[]any{"VADD", "nope", "a", "VALUES", 1, "1"}, "ERR collection not found"},
		{[]any{"VADD", "v", "a", "VALUES", 2, "1", "2"}, "ERR dimension mismatch: expected 3, got 2"},
		{[]any{"VADD", "v", "a", "FP32", encodeFP32([]float32{1, 2})}, "ERR dimension mismatch: expected 3, got 2"},
		{[]any{"VADD", "v", "a", "FP32", "abcde"}, "ERR invalid FP32 blob: length 5"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "1", "x", "3"}, "ERR invalid number in VALUES: 'x'"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "1", "2"}, "ERR VALUES 3 expects 3 numbers, got 2"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "0", "0", "0"}, "ERR zero vector is not allowed with cosine metric"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "1", "NaN", "0"}, "ERR vector contains NaN or Inf"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "1", "2", "3", "NX", "XX"}, "ERR syntax error"},
		{[]any{"VADD", "v", "a", "VALUES", 3, "1", "2", "3", "TEXT", "x"}, "ERR syntax error"},
		{[]any{"VADD", "v", "a", "VECTOR", "1"}, "ERR expected FP32 or VALUES, got 'VECTOR'"},
		{[]any{"VGET", "v", "a", "BOGUS"}, "ERR syntax error"},
	}
	for _, tc := range cases {
		wantErr(t, c.Do(tc.args...), tc.err)
	}
	want(t, c.Do("VCOUNT", "v"), ":0")
}

func TestSearch(t *testing.T) {
	c := newClient(t)
	c.Do("VCREATE", "v", "DIM", 2, "METRIC", "ip")
	c.Do("VADD", "v", "a", "VALUES", 2, "1", "0")
	c.Do("VADD", "v", "b", "VALUES", 2, "0.5", "0")
	c.Do("VADD", "v", "c", "VALUES", 2, "-1", "0")

	want(t, c.Do("VSEARCH", "v", "VALUES", 2, "1", "0", "K", 2),
		`*[*[$"a" $"1"] *[$"b" $"0.5"]]`)
	want(t, c.Do("VSEARCH", "v", "FP32", encodeFP32([]float32{1, 0}), "K", 1, "WITHVEC"),
		`*[*[$"a" $"1" $`+strconv.Quote(string(encodeFP32([]float32{1, 0})))+`]]`)

	// Default K is 10; deleted records are not returned.
	c.Do("VDEL", "v", "b")
	want(t, c.Do("VSEARCH", "v", "VALUES", 2, "1", "0"), `*[*[$"a" $"1"] *[$"c" $"-1"]]`)

	wantErr(t, c.Do("VSEARCH", "v", "VALUES", 2, "1", "0", "K", 0), "ERR K must be an integer between 1 and 10000")
	wantErr(t, c.Do("VSEARCH", "v", "VALUES", 2, "1", "0", "K"), "ERR syntax error")
	wantErr(t, c.Do("VSEARCH", "v", "VALUES", 2, "1", "0", "FILTER", "x"), "ERR syntax error")
	wantErr(t, c.Do("VSEARCH", "v", "VALUES", 1, "1"), "ERR dimension mismatch")
}

func TestSearchRESP3ScoresAreDoubles(t *testing.T) {
	c := newClient(t)
	c.Do("HELLO", "3")
	c.Do("VCREATE", "v", "DIM", 2, "METRIC", "l2")
	c.Do("VADD", "v", "a", "VALUES", 2, "3", "4")

	// l2 score is the negated distance: -5.
	want(t, c.Do("VSEARCH", "v", "VALUES", 2, "0", "0"), `*[*[$"a" ,"-5"]]`)
	want(t, c.Do("VGET", "v", "missing"), "nil")
	if v := c.Do("VINFO", "v"); v.Type != '%' {
		t.Fatalf("VINFO in RESP3 should be a map, got %s", servertest.Describe(v))
	}
}

func TestDecodeEncodeFP32(t *testing.T) {
	in := []float32{1.5, -2, 0, 3.25}
	out, err := decodeFP32(encodeFP32(in))
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		if in[i] != out[i] {
			t.Fatalf("round trip: %v != %v", out, in)
		}
	}
	// Little-endian check: 1.0 = 0x3F800000 -> 00 00 80 3F
	if b := encodeFP32([]float32{1}); string(b) != "\x00\x00\x80\x3f" {
		t.Fatalf("not little-endian: % x", b)
	}
}
