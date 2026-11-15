package store

import (
	"slices"
	"sync"

	"github.com/tranbaohieu/lawliet/internal/index"
	"github.com/tranbaohieu/lawliet/internal/meta"
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

// Record is the content of one entry. Meta is treated as immutable once stored.
type Record struct {
	Vector []float32
	Text   string
	Meta   meta.Map // nil = no metadata
}

// SearchOptions controls Search.
type SearchOptions struct {
	K           int
	Filter      func(meta.Map) bool // nil = no filter
	WithVectors bool
	WithText    bool
	WithMeta    bool
}

// Result is one search hit with its external ID. Optional fields are set
// only when requested in SearchOptions.
type Result struct {
	ID     string
	Score  float32
	Vector []float32
	Text   string
	Meta   meta.Map
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
	texts    []string
	metas    []meta.Map
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
// An update replaces the whole record: text and metadata not given are cleared.
func (c *Collection) Add(id string, rec Record, mode AddMode) (AddResult, error) {
	v, err := c.prepare(rec.Vector)
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
		c.texts[slot] = rec.Text
		c.metas[slot] = rec.Meta
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
		c.texts[slot] = rec.Text
		c.metas[slot] = rec.Meta
		c.deleted[slot] = false
	} else {
		slot = uint32(len(c.slotToID))
		c.slotToID = append(c.slotToID, id)
		c.texts = append(c.texts, rec.Text)
		c.metas = append(c.metas, rec.Meta)
		c.deleted = append(c.deleted, false)
	}
	c.vectors.Set(slot, v)
	c.idToSlot[id] = slot
	c.live++
	c.idx.Add(slot, v)
	return Inserted, nil
}

// Get returns the record. The vector is a copy (normalized for cosine).
func (c *Collection) Get(id string) (Record, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	slot, ok := c.idToSlot[id]
	if !ok {
		return Record{}, false
	}
	return Record{
		Vector: slices.Clone(c.vectors.Get(slot)),
		Text:   c.texts[slot],
		Meta:   c.metas[slot],
	}, true
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
		c.texts[slot] = "" // release memory to the GC
		c.metas[slot] = nil
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

// Search returns up to opt.K records most similar to query, best first.
// The filter runs before the distance computation, so filtered-out records cost
// only a metadata check.
func (c *Collection) Search(query []float32, opt SearchOptions) ([]Result, error) {
	q, err := c.prepare(query)
	if err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	accept := func(slot uint32) bool { return !c.deleted[slot] }
	if opt.Filter != nil {
		accept = func(slot uint32) bool { return !c.deleted[slot] && opt.Filter(c.metas[slot]) }
	}

	hits := c.idx.Search(q, opt.K, accept)
	out := make([]Result, len(hits))
	for i, h := range hits {
		r := Result{ID: c.slotToID[h.Slot], Score: h.Score}
		if opt.WithVectors {
			r.Vector = slices.Clone(c.vectors.Get(h.Slot))
		}
		if opt.WithText {
			r.Text = c.texts[h.Slot]
		}
		if opt.WithMeta {
			r.Meta = c.metas[h.Slot]
		}
		out[i] = r
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
