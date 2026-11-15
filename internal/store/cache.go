package store

import (
	"fmt"
	"math"
	"sync"

	"github.com/tranbaohieu/lawliet/internal/meta"
)

// Semantic cache support (DESIGN.md §16.1). A cache is an ordinary collection;
// these helpers back the VCACHE.* commands.

// maxCacheTTL caps CACHE_TTL at about 100 years (same cap as EX/PX).
const maxCacheTTL = 100 * 365 * 24 * 3600

func validateCacheThreshold(v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return &ConfigError{Msg: "CACHE_THRESHOLD must be a finite number"}
	}
	return nil
}

func validateCacheTTL(sec int64) error {
	if sec < 0 || sec > maxCacheTTL {
		return &ConfigError{Msg: fmt.Sprintf("CACHE_TTL must be between 0 and %d seconds", maxCacheTTL)}
	}
	return nil
}

// SetCacheThreshold sets (has=true) or clears (has=false) the default hit threshold.
func (c *Collection) SetCacheThreshold(has bool, v float64) error {
	if has {
		if err := validateCacheThreshold(v); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.HasCacheThreshold, c.cfg.CacheThreshold = has, v
	if !has {
		c.cfg.CacheThreshold = 0
	}
	c.st.dirty.Add(1)
	return nil
}

// SetCacheTTL sets the default TTL in seconds for VCACHE.SET (0 = none).
func (c *Collection) SetCacheTTL(sec int64) error {
	if err := validateCacheTTL(sec); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.CacheTTL = sec
	c.st.dirty.Add(1)
	return nil
}

// Touch marks a record as accessed now (for LRU). Returns false if it does not exist.
func (c *Collection) Touch(id string) bool {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	slot, ok, _ := c.lookup(id, now)
	if ok {
		c.touch(slot, now)
	}
	return ok
}

// DeleteWhere removes every live record whose metadata matches the filter and
// returns how many were removed. Expired records are reclaimed but not counted.
func (c *Collection) DeleteWhere(match func(meta.Map) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	n := 0
	for slot, id := range c.slotToID {
		s := uint32(slot)
		if c.deleted[s] {
			continue
		}
		if c.isExpired(s, now) {
			c.removeSlot(id, s)
			continue
		}
		if match(c.metas[s]) {
			c.removeSlot(id, s)
			n++
		}
	}
	return n
}

// CacheStats are VCACHE.GET counters, kept in memory only (reset on restart).
type CacheStats struct {
	Hits        int64
	Misses      int64
	HitScoreSum float64 // sum of scores of hits, for the average
}

// cacheStats is embedded in Collection.
type cacheStats struct {
	mu sync.Mutex
	s  CacheStats
}

// RecordCacheLookup counts one VCACHE.GET.
func (c *Collection) RecordCacheLookup(hit bool, score float32) {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()
	if hit {
		c.stats.s.Hits++
		c.stats.s.HitScoreSum += float64(score)
	} else {
		c.stats.s.Misses++
	}
}

// CacheStats returns a copy of the VCACHE.GET counters.
func (c *Collection) CacheStats() CacheStats {
	c.stats.mu.Lock()
	defer c.stats.mu.Unlock()
	return c.stats.s
}
