package store

import (
	"errors"
	"math/rand/v2"

	"github.com/tranbaohieu/lawliet/internal/meta"
)

// Memory accounting (DESIGN.md §8.2).
//
// Memory is estimated per record, not measured: the estimate is cheap,
// deterministic and needs no stop-the-world. INFO also reports the Go
// runtime figure so users can see the drift.

const (
	// recordOverhead approximates per-record bookkeeping: slotToID / texts
	// string headers, metas, expireAt, lastAccess, deleted flag and the
	// idToSlot map entry.
	recordOverhead = 96
	// metaMapOverhead approximates a Go map header with a few buckets.
	metaMapOverhead = 48
	// metaEntryOverhead approximates one map entry: key header + Value struct.
	metaEntryOverhead = 88
)

var (
	ErrOOM            = errors.New("OOM command not allowed when used memory > 'maxmemory'")
	ErrMaxVectors     = errors.New("max_vectors reached")
	ErrMaxCollections = errors.New("max collections reached")
)

// metaBytes estimates the memory held by a metadata map.
func metaBytes(m meta.Map) int64 {
	if m == nil {
		return 0
	}
	n := int64(metaMapOverhead)
	for k, v := range m {
		n += metaEntryOverhead + int64(len(k)) + int64(len(v.S))
		for _, s := range v.L {
			n += 16 + int64(len(s))
		}
	}
	return n
}

// recordBytes estimates the memory held by one record.
// The ID is counted twice: idToSlot key and slotToID.
func recordBytes(dim int, id, text string, m meta.Map) int64 {
	return int64(dim)*4 + 2*int64(len(id)) + int64(len(text)) + metaBytes(m) + recordOverhead
}

// slotBytes estimates the memory held by the record in slot. Caller must hold c.mu.
func (c *Collection) slotBytes(slot uint32) int64 {
	return recordBytes(c.cfg.Dim, c.slotToID[slot], c.texts[slot], c.metas[slot])
}

// addMem records a memory change for this collection. Caller must hold c.mu for writing.
func (c *Collection) addMem(delta int64) {
	c.memBytes += delta
	c.st.used.Add(delta)
}

// UsedMemory returns the estimated memory held by all records.
func (s *Store) UsedMemory() int64 { return s.used.Load() }

// SlabBytes returns the memory allocated by all vector slabs, including free
// slots kept for reuse and the unused part of the last chunk.
func (s *Store) SlabBytes() int64 {
	var n int64
	for _, c := range s.collectionList() {
		c.mu.RLock()
		n += c.vectors.Bytes()
		c.mu.RUnlock()
	}
	return n
}

// freeMemory makes room for need more bytes under maxmemory. It must be called
// without holding any collection lock, because it locks collections to evict.
//
//  1. Expired records are reclaimed first: removing them loses no data.
//  2. Then records are evicted from collections whose policy allows it,
//     choosing a random evictable collection each time.
//
// Another writer may use the freed memory before the caller inserts; the
// limit can then be exceeded by a few records. This is accepted for the MVP.
func (s *Store) freeMemory(need int64) error {
	limit := s.limits.Load().MaxMemory
	if limit == 0 || need <= 0 {
		return nil
	}
	if need > limit {
		return ErrOOM
	}
	over := func() bool { return s.used.Load()+need > limit }
	if !over() {
		return nil
	}

	cols := s.collectionList()
	for _, c := range cols {
		for over() {
			if _, removed := c.expireRound(); removed == 0 {
				break
			}
		}
	}

	var cands []*Collection
	for _, c := range cols {
		c.mu.RLock()
		if c.cfg.Policy != NoEviction && c.live > 0 {
			cands = append(cands, c)
		}
		c.mu.RUnlock()
	}
	for over() {
		if len(cands) == 0 {
			return ErrOOM
		}
		i := rand.IntN(len(cands))
		c := cands[i]
		c.mu.Lock()
		ok := c.evictOne()
		c.mu.Unlock()
		if !ok {
			cands = append(cands[:i], cands[i+1:]...)
		}
	}
	return nil
}
