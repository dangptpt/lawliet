package store

import (
	"context"
	"time"
)

// TTL handling (DESIGN.md §9).
//
// An expired record is invisible to every read as soon as its time passes.
// Its memory is reclaimed:
//   - lazily, when a read or write touches it, and
//   - actively, by ExpireCycle, which samples records with a TTL.

const (
	// expireSample is the number of TTL records checked per round.
	expireSample = 20
	// expireRepeatRatio: run another round while more than this share of the sample was expired.
	expireRepeatRatio = 0.25
)

// isExpired reports whether slot has a TTL that has passed. Caller must hold c.mu.
func (c *Collection) isExpired(slot uint32, now int64) bool {
	e := c.expireAt[slot]
	return e != 0 && e <= now
}

// setExpire sets the absolute expiry of slot (0 = none). Caller must hold c.mu for writing.
func (c *Collection) setExpire(slot uint32, at int64) {
	c.expireAt[slot] = at
	if at != 0 {
		c.ttlSlots[slot] = struct{}{}
	} else {
		delete(c.ttlSlots, slot)
	}
}

// reclaim removes id if it is (still) expired. Used after a read found it expired.
func (c *Collection) reclaim(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot, _, expired := c.lookup(id, c.now()); expired {
		c.removeSlot(id, slot)
	}
}

// reclaimSlots removes slots that are (still) expired. A slot may have been
// reused for a new record in between; the expiry check makes that safe.
func (c *Collection) reclaimSlots(slots []uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for _, s := range slots {
		if !c.deleted[s] && c.isExpired(s, now) {
			c.removeSlot(c.slotToID[s], s)
		}
	}
}

// TTLState is the result of TTL.
type TTLState uint8

const (
	TTLNotFound TTLState = iota // no such record (or expired)
	TTLNone                     // record exists without TTL
	TTLSet                      // record has a TTL
)

// TTL returns the remaining time to live in milliseconds.
func (c *Collection) TTL(id string) (remainingMs int64, state TTLState) {
	now := c.now()
	c.mu.RLock()
	slot, ok, expired := c.lookup(id, now)
	var at int64
	if ok {
		at = c.expireAt[slot]
	}
	c.mu.RUnlock()

	switch {
	case expired:
		c.reclaim(id)
		return 0, TTLNotFound
	case !ok:
		return 0, TTLNotFound
	case at == 0:
		return 0, TTLNone
	}
	return at - now, TTLSet
}

// Expire sets an absolute expiry time (unix ms) on id. A time in the past
// deletes the record immediately (same as Redis EXPIRE with a non-positive value).
// Returns false if the record does not exist.
func (c *Collection) Expire(id string, at int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	slot, ok, expired := c.lookup(id, now)
	if expired {
		c.removeSlot(id, slot)
	}
	if !ok {
		return false
	}
	if at <= now {
		c.removeSlot(id, slot)
		return true
	}
	c.setExpire(slot, at)
	return true
}

// Persist removes the TTL of id. Returns false if the record does not exist
// or has no TTL.
func (c *Collection) Persist(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	slot, ok, expired := c.lookup(id, c.now())
	if expired {
		c.removeSlot(id, slot)
	}
	if !ok || c.expireAt[slot] == 0 {
		return false
	}
	c.setExpire(slot, 0)
	return true
}

// expireRound checks up to expireSample TTL records and removes expired ones.
// Go randomizes map iteration order, which gives a cheap random sample.
// Returns how many were sampled and removed.
func (c *Collection) expireRound() (sampled, removed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for slot := range c.ttlSlots {
		if sampled == expireSample {
			break
		}
		sampled++
		if c.isExpired(slot, now) {
			c.removeSlot(c.slotToID[slot], slot) // deleting the current key during range is safe in Go
			removed++
		}
	}
	return sampled, removed
}

// ExpireCycle runs active expiry on every collection until the deadline.
// Per collection it repeats rounds while more than 25% of a sample was expired.
// Returns the number of records removed.
func (s *Store) ExpireCycle(deadline time.Time) int {
	total := 0
	for _, c := range s.collectionList() {
		for {
			sampled, removed := c.expireRound()
			total += removed
			if sampled == 0 || float64(removed) <= expireRepeatRatio*float64(sampled) || time.Now().After(deadline) {
				break
			}
		}
		if time.Now().After(deadline) {
			break
		}
	}
	return total
}

// RunExpirer runs ExpireCycle every interval until ctx is cancelled.
// Each cycle may use at most a quarter of the interval.
func (s *Store) RunExpirer(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ExpireCycle(time.Now().Add(interval / 4))
		}
	}
}
