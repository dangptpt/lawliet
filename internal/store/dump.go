package store

import (
	"errors"
	"fmt"

	"github.com/tranbaohieu/lawliet/internal/vecmath"
)

// Snapshot support (DESIGN.md §10).

// DumpHeader describes a collection being dumped.
type DumpHeader struct {
	Name   string
	Config Config
	Count  int // number of records that follow
}

// Dump calls header once, then record for every live, non-expired record, all
// under the collection read lock: reads continue, writes wait until it returns.
// rec.Vector aliases internal memory and must not be retained by the callback.
func (c *Collection) Dump(header func(DumpHeader) error, record func(id string, rec Record) error) error {
	now := c.now()
	c.mu.RLock()
	defer c.mu.RUnlock()

	live := func(slot int) bool { return !c.deleted[slot] && !c.isExpired(uint32(slot), now) }
	n := 0
	for slot := range c.slotToID {
		if live(slot) {
			n++
		}
	}
	if err := header(DumpHeader{Name: c.name, Config: c.cfg, Count: n}); err != nil {
		return err
	}
	for slot, id := range c.slotToID {
		if !live(slot) {
			continue
		}
		s := uint32(slot)
		err := record(id, Record{
			Vector:   c.vectors.Get(s),
			Text:     c.texts[s],
			Meta:     c.metas[s],
			ExpireAt: c.expireAt[s],
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ErrDuplicateID reports a snapshot containing the same ID twice in a collection.
var ErrDuplicateID = errors.New("duplicate record id")

// Restore inserts a record while loading a snapshot. maxmemory and
// max_vectors are not enforced (same as Redis loading an RDB file); memory is
// still accounted. Records already expired are skipped and reported as false.
//
// The vector is stored exactly as given: a snapshot holds vectors in their
// stored form (already normalized for cosine). Normalizing again would change
// the float bits of about half of them and thus the scores.
func (c *Collection) Restore(id string, rec Record) (bool, error) {
	if rec.ExpireAt != 0 && rec.ExpireAt <= c.now() {
		return false, nil
	}
	v := rec.Vector
	if len(v) != c.cfg.Dim {
		return false, &DimMismatchError{Expected: c.cfg.Dim, Got: len(v)}
	}
	if !vecmath.IsFinite(v) {
		return false, ErrNonFinite
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.idToSlot[id]; ok {
		return false, fmt.Errorf("%w %q in collection %q", ErrDuplicateID, id, c.name)
	}
	now := c.now()
	slot := uint32(len(c.slotToID))
	c.slotToID = append(c.slotToID, id)
	c.texts = append(c.texts, rec.Text)
	c.metas = append(c.metas, rec.Meta)
	c.expireAt = append(c.expireAt, 0)
	c.lastAccess = append(c.lastAccess, now)
	c.deleted = append(c.deleted, false)
	c.setExpire(slot, rec.ExpireAt)
	c.vectors.Set(slot, v)
	c.idToSlot[id] = slot
	c.live++
	c.addMem(recordBytes(c.cfg.Dim, id, rec.Text, rec.Meta))
	c.idx.Add(slot, v)
	return true, nil
}
