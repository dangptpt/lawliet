package store

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"sync/atomic"
)

// Eviction policies (DESIGN.md §8.3).

// Policy decides which record to remove when a limit is reached.
type Policy uint8

const (
	NoEviction  Policy = iota // never evict; writes fail when a limit is reached
	AllKeysLRU                // evict the least recently used record (approximate)
	VolatileTTL               // evict the record with a TTL that expires soonest (approximate)
)

func (p Policy) String() string {
	switch p {
	case NoEviction:
		return "noeviction"
	case AllKeysLRU:
		return "allkeys-lru"
	case VolatileTTL:
		return "volatile-ttl"
	}
	return fmt.Sprintf("policy(%d)", uint8(p))
}

// ParsePolicy parses a policy name (case-insensitive).
func ParsePolicy(s string) (Policy, error) {
	switch strings.ToLower(s) {
	case "noeviction":
		return NoEviction, nil
	case "allkeys-lru":
		return AllKeysLRU, nil
	case "volatile-ttl":
		return VolatileTTL, nil
	}
	return 0, fmt.Errorf("unknown policy '%s' (expected noeviction, allkeys-lru or volatile-ttl)", s)
}

// evictSamples is the number of candidates compared per eviction (N in DESIGN.md §8.3).
const evictSamples = 5

// touch marks slot as used now (for LRU). Safe under the read lock.
func (c *Collection) touch(slot uint32, now int64) {
	atomic.StoreInt64(&c.lastAccess[slot], now)
}

// evictOne removes one record chosen by the collection policy among a few
// random samples. Expired records are always preferred. Caller must hold c.mu
// for writing. Returns false if the policy allows no victim.
func (c *Collection) evictOne() bool {
	if c.live == 0 {
		return false
	}
	now := c.now()
	victim, best := -1, int64(math.MaxInt64)
	consider := func(slot uint32, score int64) {
		if c.isExpired(slot, now) {
			score = math.MinInt64
		}
		if victim < 0 || score < best {
			victim, best = int(slot), score
		}
	}

	switch c.cfg.Policy {
	case AllKeysLRU:
		n := len(c.slotToID)
		if c.live <= evictSamples { // small collection: consider every record (exact LRU)
			for slot := range c.slotToID {
				if !c.deleted[slot] {
					consider(uint32(slot), atomic.LoadInt64(&c.lastAccess[slot]))
				}
			}
			break
		}
		for picked, tries := 0, 0; picked < evictSamples && tries < evictSamples*8; tries++ {
			slot := uint32(rand.IntN(n))
			if c.deleted[slot] {
				continue
			}
			picked++
			consider(slot, atomic.LoadInt64(&c.lastAccess[slot]))
		}
		if victim < 0 { // mostly tombstones: fall back to the first live slot
			for slot := range c.slotToID {
				if !c.deleted[slot] {
					victim = slot
					break
				}
			}
		}
	case VolatileTTL:
		picked := 0
		for slot := range c.ttlSlots { // Go randomizes map iteration order
			if picked == evictSamples {
				break
			}
			picked++
			consider(slot, c.expireAt[slot])
		}
	default:
		return false
	}

	if victim < 0 {
		return false
	}
	slot := uint32(victim)
	c.removeSlot(c.slotToID[slot], slot)
	c.st.evicted.Add(1)
	return true
}

// SetPolicy changes the eviction policy.
func (c *Collection) SetPolicy(p Policy) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Policy = p
}

// SetMaxVectors changes the record limit (0 = unlimited). Lowering it below the
// current count only blocks new inserts; existing records are kept.
func (c *Collection) SetMaxVectors(n int) error {
	if n < 0 || n > MaxVectorsLimit {
		return &ConfigError{Msg: fmt.Sprintf("MAX_VECTORS must be between 0 and %d", MaxVectorsLimit)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.MaxVectors = n
	return nil
}

// makeRoom enforces max_vectors before inserting a new record. Caller must hold
// c.mu for writing. Expired records are reclaimed first, then the policy decides.
func (c *Collection) makeRoom() error {
	if c.cfg.MaxVectors == 0 || c.live < c.cfg.MaxVectors {
		return nil
	}
	now := c.now()
	checked := 0
	for slot := range c.ttlSlots { // sample, do not scan: this runs on every insert at the limit
		if checked == expireSample {
			break
		}
		checked++
		if c.isExpired(slot, now) {
			c.removeSlot(c.slotToID[slot], slot)
			return nil
		}
	}
	if c.evictOne() {
		return nil
	}
	return ErrMaxVectors
}
