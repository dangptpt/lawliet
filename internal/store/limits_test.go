package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/meta"
)

func createWith(t *testing.T, s *Store, name string, cfg Config) *Collection {
	t.Helper()
	if cfg.Index == "" {
		cfg.Index = "flat"
	}
	c, err := s.Create(name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func vec(x float32) Record { return Record{Vector: []float32{x}} }

func TestMemoryAccounting(t *testing.T) {
	s := New()
	c := createWith(t, s, "c", Config{Dim: 4, Metric: index.IP})
	m := meta.Map{"k": {Kind: meta.String, S: "value"}}

	c.Add("a", Record{Vector: []float32{1, 2, 3, 4}, Text: "hello", Meta: m}, Upsert)
	want := recordBytes(4, "a", "hello", m)
	if s.UsedMemory() != want || c.Info().MemoryBytes != want {
		t.Fatalf("after insert: store %d coll %d, want %d", s.UsedMemory(), c.Info().MemoryBytes, want)
	}

	// Update accounts only the difference.
	c.Add("a", Record{Vector: []float32{1, 2, 3, 4}, Text: "a much longer text"}, Upsert)
	want = recordBytes(4, "a", "a much longer text", nil)
	if s.UsedMemory() != want {
		t.Fatalf("after update: %d want %d", s.UsedMemory(), want)
	}

	c.Add("b", Record{Vector: []float32{1, 1, 1, 1}}, Upsert)
	c.Delete("a")
	if s.UsedMemory() != recordBytes(4, "b", "", nil) {
		t.Fatalf("after delete: %d", s.UsedMemory())
	}

	// Expiry and drop release memory too.
	c2 := createWith(t, s, "c2", Config{Dim: 4, Metric: index.IP})
	c2.Add("x", Record{Vector: []float32{1, 1, 1, 1}, ExpireAt: s.Now() - 1}, Upsert)
	c2.Exists("x") // lazy reclaim
	s.Drop("c")
	if s.UsedMemory() != 0 {
		t.Fatalf("after expiry + drop: %d", s.UsedMemory())
	}
}

func TestMaxCollections(t *testing.T) {
	s := New()
	s.SetLimits(Limits{MaxCollections: 2})
	createWith(t, s, "a", Config{Dim: 1})
	createWith(t, s, "b", Config{Dim: 1})
	if _, err := s.Create("c", Config{Dim: 1, Index: "flat"}); !errors.Is(err, ErrMaxCollections) {
		t.Fatalf("want ErrMaxCollections, got %v", err)
	}
	s.Drop("a")
	createWith(t, s, "c", Config{Dim: 1})
}

func TestMaxVectorsNoEviction(t *testing.T) {
	c := createWith(t, New(), "c", Config{Dim: 1, Metric: index.IP, MaxVectors: 2})
	c.Add("a", vec(1), Upsert)
	c.Add("b", vec(2), Upsert)
	if _, err := c.Add("c", vec(3), Upsert); !errors.Is(err, ErrMaxVectors) {
		t.Fatalf("want ErrMaxVectors, got %v", err)
	}
	// Updating an existing record is allowed at the limit.
	if r, err := c.Add("a", vec(5), Upsert); err != nil || r != Updated {
		t.Fatalf("update at limit: %v %v", r, err)
	}
	if c.Count() != 2 {
		t.Fatalf("count %d", c.Count())
	}
}

func TestMaxVectorsReclaimsExpiredFirst(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	c.SetMaxVectors(2)
	c.Add("old", Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
	c.Add("keep", vec(1), Upsert)
	clk.advance(time.Second)
	// noeviction, but the expired record frees a place.
	if _, err := c.Add("new", vec(1), Upsert); err != nil {
		t.Fatalf("insert should reuse the expired record's place: %v", err)
	}
	if !c.Exists("keep") || !c.Exists("new") || s.Evicted() != 0 {
		t.Fatal("unexpected state")
	}
}

func TestMaxVectorsLRU(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	c.SetPolicy(AllKeysLRU)
	c.SetMaxVectors(3)
	for _, id := range []string{"a", "b", "c"} {
		c.Add(id, vec(1), Upsert)
		clk.advance(time.Second)
	}
	// With at most evictSamples records, every record is considered: exact LRU.
	c.Get("a") // a becomes the most recently used; b is now the oldest
	clk.advance(time.Second)

	if _, err := c.Add("d", vec(1), Upsert); err != nil {
		t.Fatal(err)
	}
	if c.Count() != 3 || s.Evicted() != 1 {
		t.Fatalf("count %d evicted %d", c.Count(), s.Evicted())
	}
	if c.Exists("b") {
		t.Fatal("b was least recently used and should be evicted")
	}
}

func TestSearchCountsAsAccess(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.SetPolicy(AllKeysLRU)
	c.SetMaxVectors(2)
	c.Add("hit", Record{Vector: []float32{1}}, Upsert)
	clk.advance(time.Second)
	c.Add("miss", Record{Vector: []float32{-1}}, Upsert)
	clk.advance(time.Second)
	c.Search([]float32{1}, SearchOptions{K: 1}) // returns "hit" only
	clk.advance(time.Second)
	c.Add("new", vec(1), Upsert)
	if !c.Exists("hit") || c.Exists("miss") {
		t.Fatal("record returned by search should be kept; the other evicted")
	}
}

func TestMaxVectorsVolatileTTL(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.SetPolicy(VolatileTTL)
	c.SetMaxVectors(3)
	c.Add("soon", Record{Vector: []float32{1}, ExpireAt: clk.now() + 1000}, Upsert)
	c.Add("later", Record{Vector: []float32{1}, ExpireAt: clk.now() + 9000}, Upsert)
	c.Add("forever", vec(1), Upsert)

	c.Add("d", vec(1), Upsert)
	if c.Exists("soon") || !c.Exists("later") || !c.Exists("forever") {
		t.Fatal("volatile-ttl should evict the record expiring soonest")
	}
	c.Add("e", vec(1), Upsert) // evicts "later", the only TTL record
	if c.Exists("later") {
		t.Fatal("later should be evicted")
	}
	// No TTL records left: volatile-ttl has no victim.
	if _, err := c.Add("f", vec(1), Upsert); !errors.Is(err, ErrMaxVectors) {
		t.Fatalf("want ErrMaxVectors, got %v", err)
	}
}

func TestLoweringMaxVectorsOnlyBlocksInserts(t *testing.T) {
	c := createWith(t, New(), "c", Config{Dim: 1, Metric: index.IP})
	for i := 0; i < 5; i++ {
		c.Add(fmt.Sprint(i), vec(1), Upsert)
	}
	if err := c.SetMaxVectors(2); err != nil {
		t.Fatal(err)
	}
	if c.Count() != 5 {
		t.Fatal("lowering max_vectors must not delete records")
	}
	if _, err := c.Add("new", vec(1), Upsert); !errors.Is(err, ErrMaxVectors) {
		t.Fatalf("want ErrMaxVectors, got %v", err)
	}
	if err := c.SetMaxVectors(-1); err == nil {
		t.Fatal("negative max_vectors should fail")
	}
}

func TestMaxMemoryNoEviction(t *testing.T) {
	s := New()
	per := recordBytes(1, "0", "", nil)
	s.SetLimits(Limits{MaxMemory: 3 * per})
	c := createWith(t, s, "c", Config{Dim: 1, Metric: index.IP})
	for i := 0; i < 3; i++ {
		if _, err := c.Add(fmt.Sprint(i), vec(1), Upsert); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := c.Add("3", vec(1), Upsert); !errors.Is(err, ErrOOM) {
		t.Fatalf("want ErrOOM, got %v", err)
	}
	// Same-size update needs no extra memory.
	if _, err := c.Add("0", vec(2), Upsert); err != nil {
		t.Fatalf("same-size update: %v", err)
	}
	// A single record larger than maxmemory fails without evicting anything.
	if _, err := c.Add("big", Record{Vector: []float32{1}, Text: string(make([]byte, 10*per))}, Upsert); !errors.Is(err, ErrOOM) {
		t.Fatalf("oversized record: %v", err)
	}
	if c.Count() != 3 {
		t.Fatalf("count %d", c.Count())
	}
}

func TestMaxMemoryEvictsFromCacheOnly(t *testing.T) {
	s := New()
	per := recordBytes(1, "d0", "", nil) // ids below all have 2 chars
	s.SetLimits(Limits{MaxMemory: 10 * per})
	docs := createWith(t, s, "docs", Config{Dim: 1, Metric: index.IP}) // noeviction
	cache := createWith(t, s, "cache", Config{Dim: 1, Metric: index.IP, Policy: AllKeysLRU})

	for i := 0; i < 8; i++ {
		cache.Add(fmt.Sprintf("c%d", i), vec(1), Upsert)
	}
	// Documents push the cache out; documents are never evicted.
	for i := 0; i < 6; i++ {
		if _, err := docs.Add(fmt.Sprintf("d%d", i), vec(1), Upsert); err != nil {
			t.Fatalf("doc %d: %v", i, err)
		}
	}
	if docs.Count() != 6 || cache.Count() != 4 {
		t.Fatalf("docs %d cache %d", docs.Count(), cache.Count())
	}
	if s.UsedMemory() > 10*per {
		t.Fatalf("used %d > limit %d", s.UsedMemory(), 10*per)
	}
	// When the cache is empty, docs get OOM.
	for i := 6; i < 10; i++ {
		docs.Add(fmt.Sprintf("d%d", i), vec(1), Upsert)
	}
	if cache.Count() != 0 {
		t.Fatalf("cache should be empty, has %d", cache.Count())
	}
	if _, err := docs.Add("dx", vec(1), Upsert); !errors.Is(err, ErrOOM) {
		t.Fatalf("want ErrOOM, got %v", err)
	}
	if s.Evicted() != 8 {
		t.Fatalf("evicted %d", s.Evicted())
	}
}

func TestMaxMemoryReclaimsExpiredBeforeEvicting(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	per := recordBytes(1, "a0", "", nil)
	s.SetLimits(Limits{MaxMemory: 4 * per})
	c.Add("a0", Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
	c.Add("a1", Record{Vector: []float32{1}, ExpireAt: clk.now() + 10}, Upsert)
	c.Add("k0", vec(1), Upsert)
	c.Add("k1", vec(1), Upsert)
	clk.advance(time.Second)

	// noeviction collection, but expired records free memory.
	if _, err := c.Add("n0", vec(1), Upsert); err != nil {
		t.Fatalf("expired records should be reclaimed: %v", err)
	}
	if !c.Exists("k0") || !c.Exists("k1") || s.Evicted() != 0 {
		t.Fatal("live records must be kept")
	}
}

func TestPolicyParse(t *testing.T) {
	for _, p := range []Policy{NoEviction, AllKeysLRU, VolatileTTL} {
		got, err := ParsePolicy(p.String())
		if err != nil || got != p {
			t.Fatalf("%s: %v %v", p, got, err)
		}
	}
	if _, err := ParsePolicy("allkeys-random"); err == nil {
		t.Fatal("unknown policy should fail")
	}
}

func TestApproximateLRUKeepsHotRecords(t *testing.T) {
	s, c, clk := newTTLCollection(t)
	c.SetPolicy(AllKeysLRU)
	const n = 2000
	c.SetMaxVectors(n)
	for i := 0; i < n; i++ {
		c.Add(fmt.Sprintf("r%d", i), vec(1), Upsert)
	}
	clk.advance(time.Second)
	// 10% of records are "hot": accessed after all others were written.
	for i := 0; i < n/10; i++ {
		c.Get(fmt.Sprintf("r%d", i))
	}
	clk.advance(time.Second)
	// Insert 25% new records, forcing 500 evictions.
	for i := 0; i < n/4; i++ {
		c.Add(fmt.Sprintf("new%d", i), vec(1), Upsert)
		clk.advance(time.Millisecond)
	}
	hotKept := 0
	for i := 0; i < n/10; i++ {
		if c.Exists(fmt.Sprintf("r%d", i)) {
			hotKept++
		}
	}
	if s.Evicted() != n/4 || c.Count() != n {
		t.Fatalf("evicted %d count %d", s.Evicted(), c.Count())
	}
	// Uniform random eviction would keep ~75% of hot records; sampled LRU keeps nearly all.
	if hotKept < n/10*95/100 {
		t.Fatalf("only %d/%d hot records kept", hotKept, n/10)
	}
	t.Logf("hot records kept: %d/%d", hotKept, n/10)
}
