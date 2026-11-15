package store

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/tranbaohieu/lawliet/internal/filter"
	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/meta"
)

func TestCacheConfig(t *testing.T) {
	s := New()
	c, err := s.Create("c", Config{Dim: 1, Index: "flat", HasCacheThreshold: true, CacheThreshold: 0.9, CacheTTL: 60})
	if err != nil {
		t.Fatal(err)
	}
	if cfg := c.Config(); !cfg.HasCacheThreshold || cfg.CacheThreshold != 0.9 || cfg.CacheTTL != 60 {
		t.Fatalf("config %+v", cfg)
	}
	if err := c.SetCacheThreshold(false, 0); err != nil || c.Config().HasCacheThreshold {
		t.Fatalf("clear threshold: %v %+v", err, c.Config())
	}
	if err := c.SetCacheTTL(0); err != nil || c.Config().CacheTTL != 0 {
		t.Fatalf("clear ttl: %v", err)
	}

	var ce *ConfigError
	if err := c.SetCacheThreshold(true, math.NaN()); !errors.As(err, &ce) {
		t.Fatalf("NaN threshold: %v", err)
	}
	if err := c.SetCacheTTL(-1); !errors.As(err, &ce) {
		t.Fatalf("negative ttl: %v", err)
	}
	if _, err := s.Create("bad", Config{Dim: 1, Index: "flat", CacheTTL: maxCacheTTL + 1}); !errors.As(err, &ce) {
		t.Fatalf("huge ttl: %v", err)
	}
}

func TestSearchNoTouchAndTouch(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	c.SetPolicy(AllKeysLRU)
	c.SetMaxVectors(2)
	c.Add("near", Record{Vector: []float32{1}}, Upsert)
	clk.advance(time.Second)
	c.Add("other", Record{Vector: []float32{-1}}, Upsert)
	clk.advance(time.Second)

	// A lookup that does not use its result must not keep "near" alive.
	c.Search([]float32{1}, SearchOptions{K: 1, NoTouch: true})
	clk.advance(time.Second)
	c.Add("new", vec(1), Upsert)
	if c.Exists("near") {
		t.Fatal("NoTouch search should not count as access: near should be evicted")
	}

	if !c.Touch("other") || c.Touch("missing") {
		t.Fatal("Touch return values")
	}
}

func TestDeleteWhere(t *testing.T) {
	_, c, clk := newTTLCollection(t)
	for i := 0; i < 10; i++ {
		m := meta.Map{"model": {Kind: meta.String, S: fmt.Sprintf("m%d", i%2)}}
		c.Add(fmt.Sprint(i), Record{Vector: []float32{1}, Meta: m}, Upsert)
	}
	c.Add("nometa", vec(1), Upsert)
	c.Add("expired", Record{Vector: []float32{1}, Meta: meta.Map{"model": {Kind: meta.String, S: "m0"}},
		ExpireAt: clk.now() + 10}, Upsert)
	clk.advance(time.Second)

	f, _ := filter.Parse(`model == "m0"`)
	if n := c.DeleteWhere(f.Match); n != 5 {
		t.Fatalf("deleted %d, want 5 (expired record not counted)", n)
	}
	if c.Count() != 6 || c.Exists("0") || !c.Exists("1") || !c.Exists("nometa") {
		t.Fatalf("unexpected state, count %d", c.Count())
	}
}

func TestCacheStatsCounters(t *testing.T) {
	c := mustCreate(t, New(), "c", 1, index.IP)
	c.RecordCacheLookup(true, 0.9)
	c.RecordCacheLookup(true, 0.7)
	c.RecordCacheLookup(false, 0.2)
	st := c.CacheStats()
	if st.Hits != 2 || st.Misses != 1 || math.Abs(st.HitScoreSum-1.6) > 1e-6 {
		t.Fatalf("%+v", st)
	}
}
