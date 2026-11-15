package command

import (
	"strings"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/store"
)

func TestVCacheGetSet(t *testing.T) {
	clk, _, do := newTTLClient(t) // collection "v": DIM 1, METRIC ip
	_ = clk
	do("VCREATE", "c", "DIM", 2, "CACHE_THRESHOLD", "0.9", "CACHE_TTL", 60)

	// Empty cache: miss.
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "0"), "nil")

	expect(t, do("VCACHE.SET", "c", "q1", "VALUES", 2, "1", "0", "TEXT", "On the 5th",
		"META", `{"model":"m1"}`), ":1")
	expect(t, do("VTTL", "c", "q1"), ":60") // CACHE_TTL applied
	expect(t, do("VCACHE.SET", "c", "q2", "VALUES", 2, "0", "1", "TEXT", "Other", "EX", 5), ":1")
	expect(t, do("VTTL", "c", "q2"), ":5") // explicit EX wins

	// Same direction: cosine 1 >= 0.9 -> hit.
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "2", "0"), `*[$"q1" $"1" $"On the 5th"]`)
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "0", "WITHMETA"), `*[$"q1" $"1" $"On the 5th" $"{\"model\":\"m1\"}"]`)
	// cos(45°) ≈ 0.707 < 0.9 -> miss.
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "1"), "nil")
	// A lower per-call THRESHOLD turns it into a hit.
	if got := do("VCACHE.GET", "c", "VALUES", 2, "1", "1", "THRESHOLD", "0.7"); !strings.HasPrefix(got, `*[$"q`) {
		t.Fatalf("THRESHOLD 0.7: %s", got)
	}
	// FILTER scopes the cache.
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "0", "FILTER", `model == "m2"`), "nil")
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "0", "FILTER", `model == "m1"`), `*[$"q1" $"1" $"On the 5th"]`)
}

func TestVCacheStatsAndInvalidate(t *testing.T) {
	_, _, do := newTTLClient(t)
	do("VCREATE", "c", "DIM", 2, "CACHE_THRESHOLD", "0.9")
	expect(t, do("VCACHE.STATS", "c"),
		`*[$"hits" :0 $"misses" :0 $"hit_rate" $"0" $"avg_hit_score" $"0" $"entries" :0]`)

	do("VCACHE.SET", "c", "a", "VALUES", 2, "1", "0", "TEXT", "A", "META", `{"model":"old"}`)
	do("VCACHE.SET", "c", "b", "VALUES", 2, "0", "1", "TEXT", "B", "META", `{"model":"new"}`)
	do("VCACHE.GET", "c", "VALUES", 2, "1", "0") // hit, score 1
	do("VCACHE.GET", "c", "VALUES", 2, "0", "1") // hit, score 1
	do("VCACHE.GET", "c", "VALUES", 2, "1", "1") // miss
	expect(t, do("VCACHE.STATS", "c"),
		`*[$"hits" :2 $"misses" :1 $"hit_rate" $"0.6666666666666666" $"avg_hit_score" $"1" $"entries" :2]`)

	expect(t, do("VCACHE.INVALIDATE", "c", "FILTER", `model == "old"`), ":1")
	expect(t, do("VEXISTS", "c", "a"), ":0")
	expect(t, do("VEXISTS", "c", "b"), ":1")
	expect(t, do("VCACHE.INVALIDATE", "c", "FILTER", `model == "nothing"`), ":0")
}

func TestVCacheOnlyHitsCountForLRU(t *testing.T) {
	clk, _, do := newTTLClient(t)
	do("VCREATE", "c", "DIM", 2, "MAX_VECTORS", 2, "POLICY", "allkeys-lru", "CACHE_THRESHOLD", "0.99")
	do("VCACHE.SET", "c", "near", "VALUES", 2, "1", "0", "TEXT", "x")
	clk.advance(time.Second)
	do("VCACHE.SET", "c", "far", "VALUES", 2, "-1", "0", "TEXT", "y")
	clk.advance(time.Second)
	// Miss whose best candidate is "near": must not keep "near" alive.
	expect(t, do("VCACHE.GET", "c", "VALUES", 2, "1", "0.5"), "nil")
	clk.advance(time.Second)
	do("VCACHE.SET", "c", "new", "VALUES", 2, "0", "1", "TEXT", "z")
	expect(t, do("VEXISTS", "c", "near"), ":0")
	expect(t, do("VEXISTS", "c", "far"), ":1")

	// A hit does count: "far" survives the next eviction, "new" goes.
	clk.advance(time.Second)
	do("VCACHE.GET", "c", "VALUES", 2, "-1", "0") // hit on far
	clk.advance(time.Second)
	do("VCACHE.SET", "c", "newer", "VALUES", 2, "0", "-1", "TEXT", "w")
	expect(t, do("VEXISTS", "c", "far"), ":1")
	expect(t, do("VEXISTS", "c", "new"), ":0")
}

func TestVCacheConfigAndErrors(t *testing.T) {
	_, st, do := newTTLClient(t)
	do("VCREATE", "c", "DIM", 1)
	expect(t, do("VCACHE.GET", "c", "VALUES", 1, "1"),
		`-"ERR no threshold: pass THRESHOLD or set CACHE_THRESHOLD on the collection"`)

	expect(t, do("VCONFIG", "c", "SET", "CACHE_THRESHOLD", "0.95", "CACHE_TTL", 120), `+"OK"`)
	expect(t, do("VCONFIG", "c", "GET"),
		`*[$"max_vectors" :0 $"policy" $"noeviction" $"cache_threshold" $"0.95" $"cache_ttl" :120]`)
	expect(t, do("VCONFIG", "c", "SET", "CACHE_THRESHOLD", "NONE", "CACHE_TTL", 0), `+"OK"`)
	if cfg := mustGet(t, st, "c").Config(); cfg.HasCacheThreshold || cfg.CacheTTL != 0 {
		t.Fatalf("cleared config: %+v", cfg)
	}

	errs := []struct {
		args []any
		want string
	}{
		{[]any{"VCACHE.SET", "c", "a", "VALUES", 1, "1"}, `-"ERR VCACHE.SET requires a non-empty TEXT`},
		{[]any{"VCACHE.GET", "c", "VALUES", 1, "1", "THRESHOLD", "abc"}, `-"ERR THRESHOLD must be a finite number"`},
		{[]any{"VCACHE.GET", "c", "VALUES", 1, "1", "THRESHOLD", "NaN"}, `-"ERR THRESHOLD must be a finite number"`},
		{[]any{"VCACHE.GET", "c", "VALUES", 1, "1", "THRESHOLD", 1, "WITHVEC"}, `-"ERR syntax error"`},
		{[]any{"VCACHE.INVALIDATE", "c", "WHERE", "x == 1"}, `-"ERR syntax error"`},
		{[]any{"VCACHE.INVALIDATE", "c", "FILTER", "x = 1"}, `-"ERR invalid filter`},
		{[]any{"VCACHE.STATS", "nope"}, `-"ERR collection not found"`},
		{[]any{"VCREATE", "x", "DIM", 1, "CACHE_THRESHOLD", "inf"}, `-"ERR CACHE_THRESHOLD must be a finite number"`},
		{[]any{"VCREATE", "x", "DIM", 1, "CACHE_TTL", -1}, `-"ERR CACHE_TTL must be an integer`},
		{[]any{"VCONFIG", "c", "SET", "CACHE_THRESHOLD", "x"}, `-"ERR CACHE_THRESHOLD must be a finite number"`},
	}
	for _, e := range errs {
		if got := do(e.args...); !strings.HasPrefix(got, e.want) {
			t.Errorf("%v: got %s, want prefix %s", e.args, got, e.want)
		}
	}
}

func mustGet(t *testing.T, st *store.Store, name string) *store.Collection {
	t.Helper()
	c, err := st.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
