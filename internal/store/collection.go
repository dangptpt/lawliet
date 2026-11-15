package store

import (
	"slices"
	"sync"

	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/vecmath"
)

// MaxDim is the largest supported vector dimension.
const MaxDim = 65536

// Config is the immutable configuration of a collection.
type Config struct {
	Dim    int
	Metric index.Metric
	Index  string // "flat" (MVP)
}

func (c Config) validate() error {
	if c.Dim < 1 || c.Dim > MaxDim {
		return &ConfigError{Msg: "DIM must be between 1 and 65536"}
	}
	switch c.Metric {
	case index.Cosine, index.L2, index.IP:
	default:
		return &ConfigError{Msg: "invalid metric"}
	}
	if c.Index != "flat" {
		return &ConfigError{Msg: "unsupported index '" + c.Index + "' (MVP supports: flat)"}
	}
	return nil
}

// AddMode controls VADD behavior for existing IDs.
type AddMode uint8

const (
	Upsert       AddMode = iota // insert or overwrite
	OnlyNew                     // NX: insert only if absent
	OnlyExisting                // XX: overwrite only if present
)

// AddResult reports what Add did.
type AddResult uint8

const (
	Inserted AddResult = iota
	Updated
	Skipped // NX/XX condition not met
)

// Result is one search hit with its external ID.
type Result struct {
	ID     string
	Score  float32
	Vector []float32 // set only when requested
}

// Info is a snapshot of collection statistics.
type Info struct {
	Name        string
	Config      Config
	Count       int
	Slots       int   // allocated slots, including tombstones
	VectorBytes int64 // memory held by the vector slab
}

// Collection holds records of one vector space.
//
// Records are addressed by a user-defined string ID mapped to an internal
// uint32 slot. Per-slot data lives in parallel slices indexed by slot.
// Deleted slots become tombstones and are reused through freeList.
type Collection struct {
	mu   sync.RWMutex
	name string
	cfg  Config

	vectors  *VectorSlab
	idToSlot map[string]uint32
	slotToID []string
	deleted  []bool
	freeList []uint32
	live     int

	idx index.Index
}

func newCollection(name string, cfg Config) *Collection {
	c := &Collection{
		name:     name,
		cfg:      cfg,
		vectors:  NewVectorSlab(cfg.Dim),
		idToSlot: make(map[string]uint32),
	}
	c.idx = index.NewFlat(c, cfg.Metric)
	return c
}

// NumSlots implements index.Source. Caller must hold c.mu.
func (c *Collection) NumSlots() int { return len(c.slotToID) }

// Vector implements index.Source. Caller must hold c.mu.
func (c *Collection) Vector(slot uint32) []float32 { return c.vectors.Get(slot) }

// Name returns the collection name.
func (c *Collection) Name() string { return c.name }

// Config returns the collection configuration.
func (c *Collection) Config() Config { return c.cfg }

// prepare validates v and returns the form to store or query with.
// For cosine the vector is normalized into a copy; v itself is never modified.
func (c *Collection) prepare(v []float32) ([]float32, error) {
	if len(v) != c.cfg.Dim {
		return nil, &DimMismatchError{Expected: c.cfg.Dim, Got: len(v)}
	}
	if !vecmath.IsFinite(v) {
		return nil, ErrNonFinite
	}
	if c.cfg.Metric == index.Cosine {
		v = slices.Clone(v)
		if !vecmath.Normalize(v) {
			return nil, ErrZeroVector
		}
	}
	return v, nil
}

// Add inserts or updates the record id according to mode.
func (c *Collection) Add(id string, vec []float32, mode AddMode) (AddResult, error) {
	v, err := c.prepare(vec)
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if slot, ok := c.idToSlot[id]; ok {
		if mode == OnlyNew {
			return Skipped, nil
		}
		c.idx.Remove(slot)
		c.vectors.Set(slot, v)
		c.idx.Add(slot, v)
		return Updated, nil
	}
	if mode == OnlyExisting {
		return Skipped, nil
	}

	var slot uint32
	if n := len(c.freeList); n > 0 {
		slot = c.freeList[n-1]
		c.freeList = c.freeList[:n-1]
		c.slotToID[slot] = id
		c.deleted[slot] = false
	} else {
		slot = uint32(len(c.slotToID))
		c.slotToID = append(c.slotToID, id)
		c.deleted = append(c.deleted, false)
	}
	c.vectors.Set(slot, v)
	c.idToSlot[id] = slot
	c.live++
	c.idx.Add(slot, v)
	return Inserted, nil
}

// Get returns a copy of the stored vector (normalized for cosine).
func (c *Collection) Get(id string) ([]float32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	slot, ok := c.idToSlot[id]
	if !ok {
		return nil, false
	}
	return slices.Clone(c.vectors.Get(slot)), true
}

// Exists reports whether id is present.
func (c *Collection) Exists(id string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.idToSlot[id]
	return ok
}

// Delete removes the given IDs and returns how many existed.
func (c *Collection) Delete(ids ...string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, id := range ids {
		slot, ok := c.idToSlot[id]
		if !ok {
			continue
		}
		c.idx.Remove(slot)
		delete(c.idToSlot, id)
		c.slotToID[slot] = ""
		c.deleted[slot] = true
		c.freeList = append(c.freeList, slot)
		c.live--
		n++
	}
	return n
}

// Count returns the number of live records.
func (c *Collection) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.live
}

// Search returns up to k records most similar to query, best first.
func (c *Collection) Search(query []float32, k int, withVectors bool) ([]Result, error) {
	q, err := c.prepare(query)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	hits := c.idx.Search(q, k, func(slot uint32) bool { return !c.deleted[slot] })
	out := make([]Result, len(hits))
	for i, h := range hits {
		out[i] = Result{ID: c.slotToID[h.Slot], Score: h.Score}
		if withVectors {
			out[i].Vector = slices.Clone(c.vectors.Get(h.Slot))
		}
	}
	return out, nil
}

// Info returns collection statistics.
func (c *Collection) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Info{
		Name:        c.name,
		Config:      c.cfg,
		Count:       c.live,
		Slots:       len(c.slotToID),
		VectorBytes: c.vectors.Bytes(),
	}
}
