package store

// ChunkSize is the number of vectors per slab chunk.
const ChunkSize = 1024

// VectorSlab stores fixed-size float32 vectors in fixed-size chunks.
//
//   - Chunks contain no pointers, so the GC does not scan their contents.
//   - Growing allocates one new chunk; existing data is never copied,
//     so memory grows smoothly instead of doubling like append (DESIGN.md §3.3).
type VectorSlab struct {
	dim    int
	chunks [][]float32
}

// NewVectorSlab returns an empty slab for vectors of length dim.
func NewVectorSlab(dim int) *VectorSlab {
	return &VectorSlab{dim: dim}
}

// Get returns the vector stored in slot. The slot must be within Capacity.
// The returned slice aliases slab memory; callers must copy it before releasing locks.
func (s *VectorSlab) Get(slot uint32) []float32 {
	c := s.chunks[slot/ChunkSize]
	off := int(slot%ChunkSize) * s.dim
	return c[off : off+s.dim : off+s.dim]
}

// Set copies v into slot, allocating chunks as needed.
func (s *VectorSlab) Set(slot uint32, v []float32) {
	for int(slot/ChunkSize) >= len(s.chunks) {
		s.chunks = append(s.chunks, make([]float32, ChunkSize*s.dim))
	}
	copy(s.Get(slot), v)
}

// Capacity returns the number of slots currently allocated.
func (s *VectorSlab) Capacity() int { return len(s.chunks) * ChunkSize }

// Bytes returns the memory held by chunk data.
func (s *VectorSlab) Bytes() int64 { return int64(len(s.chunks)) * ChunkSize * int64(s.dim) * 4 }
