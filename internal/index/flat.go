package index

import (
	"math"

	"github.com/tranbaohieu/lawliet/internal/vecmath"
)

// Flat is an exact (brute-force) index: every accepted vector is scored.
type Flat struct {
	src    Source
	metric Metric
}

// NewFlat returns a flat index over src.
func NewFlat(src Source, metric Metric) *Flat {
	return &Flat{src: src, metric: metric}
}

// Add is a no-op: the flat index reads vectors directly from the source.
func (f *Flat) Add(uint32, []float32) {}

// Remove is a no-op: removed slots are rejected by the accept function.
func (f *Flat) Remove(uint32) {}

// Search scores all accepted slots and keeps the best k with a min-heap.
func (f *Flat) Search(query []float32, k int, accept func(slot uint32) bool) []Hit {
	if k <= 0 {
		return nil
	}
	// For L2 the heap ranks by -squared distance (same order, no sqrt per vector).
	score := func(v []float32) float32 { return vecmath.Dot(query, v) }
	if f.metric == L2 {
		score = func(v []float32) float32 { return -vecmath.L2Sq(query, v) }
	}

	top := newTopK(k)
	n := f.src.NumSlots()
	for i := 0; i < n; i++ {
		slot := uint32(i)
		if accept != nil && !accept(slot) {
			continue
		}
		top.push(Hit{Slot: slot, Score: score(f.src.Vector(slot))})
	}
	hits := top.sorted()

	if f.metric == L2 {
		for i := range hits {
			hits[i].Score = -float32(math.Sqrt(float64(-hits[i].Score)))
		}
	}
	return hits
}
