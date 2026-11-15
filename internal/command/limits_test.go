package command

import (
	"strings"
	"testing"

	"github.com/tranbaohieu/lawliet/internal/server"
	"github.com/tranbaohieu/lawliet/internal/server/servertest"
	"github.com/tranbaohieu/lawliet/internal/store"
)

func TestVCreateLimitsAndVConfig(t *testing.T) {
	st := store.New()
	st.SetLimits(store.Limits{DefaultPolicy: store.AllKeysLRU})
	c := newClientWithStore(t, st)
	do := func(args ...any) string { return describe(c.Do(args...)) }

	expect(t, do("VCREATE", "a", "DIM", 1), `+"OK"`) // default policy from server
	expect(t, do("VCONFIG", "a", "GET"), `*[$"max_vectors" :0 $"policy" $"allkeys-lru" $"cache_threshold" nil $"cache_ttl" :0]`)

	expect(t, do("VCREATE", "b", "DIM", 1, "MAX_VECTORS", 2, "POLICY", "noeviction"), `+"OK"`)
	expect(t, do("VCONFIG", "b", "GET"), `*[$"max_vectors" :2 $"policy" $"noeviction" $"cache_threshold" nil $"cache_ttl" :0]`)

	expect(t, do("VCONFIG", "b", "SET", "POLICY", "volatile-ttl", "MAX_VECTORS", 10), `+"OK"`)
	expect(t, do("VCONFIG", "b", "GET"), `*[$"max_vectors" :10 $"policy" $"volatile-ttl" $"cache_threshold" nil $"cache_ttl" :0]`)

	errs := []struct {
		args []any
		want string
	}{
		{[]any{"VCREATE", "x", "DIM", 1, "POLICY", "lfu"}, `-"ERR unknown policy 'lfu'`},
		{[]any{"VCREATE", "x", "DIM", 1, "MAX_VECTORS", -1}, `-"ERR MAX_VECTORS must be an integer between 0 and 2147483647"`},
		{[]any{"VCONFIG", "b", "SET", "DIM", 3}, `-"ERR unknown or immutable parameter 'DIM'`},
		{[]any{"VCONFIG", "b", "SET", "POLICY"}, `-"ERR syntax error"`},
		{[]any{"VCONFIG", "b", "SET", "MAX_VECTORS", "x"}, `-"ERR MAX_VECTORS must be an integer`},
		{[]any{"VCONFIG", "b", "BOGUS"}, `-"ERR syntax error"`},
		{[]any{"VCONFIG", "nope", "GET"}, `-"ERR collection not found"`},
	}
	for _, e := range errs {
		if got := do(e.args...); !strings.HasPrefix(got, e.want) {
			t.Errorf("%v: got %s, want prefix %s", e.args, got, e.want)
		}
	}
	// A failed SET changes nothing (all values validated first).
	do("VCONFIG", "b", "SET", "POLICY", "noeviction", "MAX_VECTORS", "x")
	expect(t, do("VCONFIG", "b", "GET"), `*[$"max_vectors" :10 $"policy" $"volatile-ttl" $"cache_threshold" nil $"cache_ttl" :0]`)
}

func TestMaxVectorsCommands(t *testing.T) {
	c := newClient(t)
	do := func(args ...any) string { return describe(c.Do(args...)) }
	do("VCREATE", "v", "DIM", 1, "MAX_VECTORS", 2)
	expect(t, do("VADD", "v", "a", "VALUES", 1, "1"), ":1")
	expect(t, do("VADD", "v", "b", "VALUES", 1, "1"), ":1")
	expect(t, do("VADD", "v", "c", "VALUES", 1, "1"), `-"ERR max_vectors reached"`)
	expect(t, do("VADD", "v", "a", "VALUES", 1, "2"), ":0") // update allowed

	expect(t, do("VCONFIG", "v", "SET", "POLICY", "allkeys-lru"), `+"OK"`)
	expect(t, do("VADD", "v", "c", "VALUES", 1, "1"), ":1")
	expect(t, do("VCOUNT", "v"), ":2")

	// Lowering below the count keeps records.
	expect(t, do("VCONFIG", "v", "SET", "MAX_VECTORS", 1, "POLICY", "noeviction"), `+"OK"`)
	expect(t, do("VCOUNT", "v"), ":2")
	expect(t, do("VADD", "v", "d", "VALUES", 1, "1"), `-"ERR max_vectors reached"`)
}

func TestMaxMemoryAndInfo(t *testing.T) {
	st := store.New()
	st.SetLimits(store.Limits{MaxMemory: 1000, MaxCollections: 2})
	addr := servertest.Start(t, func(s *server.Server) {
		Register(s, st)
		RegisterInfo(s, st)
	})
	c := servertest.Dial(t, addr)
	do := func(args ...any) string { return describe(c.Do(args...)) }

	do("VCREATE", "docs", "DIM", 1)
	do("VCREATE", "cache", "DIM", 1, "POLICY", "allkeys-lru")
	expect(t, do("VCREATE", "third", "DIM", 1), `-"ERR max collections reached"`)

	// Each record is ~104 bytes: fill the cache, then documents push it out.
	for i := 0; i < 9; i++ {
		do("VADD", "cache", "c"+string(rune('0'+i)), "VALUES", 1, "1")
	}
	for i := 0; i < 9; i++ {
		if got := do("VADD", "docs", "d"+string(rune('0'+i)), "VALUES", 1, "1"); got != ":1" {
			t.Fatalf("doc %d: %s", i, got)
		}
	}
	expect(t, do("VCOUNT", "docs"), ":9")
	expect(t, do("VCOUNT", "cache"), ":0")
	if got := do("VADD", "docs", "dx", "VALUES", 1, "1"); !strings.HasPrefix(got, `-"OOM command not allowed`) {
		t.Fatalf("want -OOM, got %s", got)
	}
	// Deletes and reads still work at the limit.
	expect(t, do("VDEL", "docs", "d0"), ":1")
	expect(t, do("VADD", "docs", "dx", "VALUES", 1, "1"), ":1")

	info := c.Do("INFO").Str
	for _, want := range []string{
		"used_memory_estimated:", "maxmemory:1000", "maxmemory_policy:noeviction",
		"evicted_keys:9", "# Keyspace", "collections:2", "max_collections:2",
	} {
		if !strings.Contains(info, want) {
			t.Errorf("INFO missing %q:\n%s", want, info)
		}
	}
	if mem := c.Do("INFO", "memory").Str; strings.Contains(mem, "# Stats") || !strings.Contains(mem, "used_memory_estimated") {
		t.Errorf("INFO memory:\n%s", mem)
	}
}
