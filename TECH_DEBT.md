# Technical Debt

Known limitations accepted for the MVP, to fix later. Each entry says why it was
deferred, how it shows up, and what a fix looks like.

---

## TD-1: `maxmemory` does not bound process memory

- **Recorded:** 2026-11-15 (M4)
- **Status:** open — deferred to Phase 2 (decision by the maintainer)
- **Design reference:** DESIGN.md §8.2

### Problem

`maxmemory` limits the *estimated logical size* of records, not the memory the
process actually uses. Measured with dim 1536 and `--maxmemory 20mb`
(`scripts/e2e/m4_limits.py`):

| `INFO memory` field          | Value    |
|------------------------------|----------|
| `used_memory_estimated`      | 20.0 MiB |
| `used_memory_vector_slab`    | 30.0 MiB |
| `used_memory_heap_live`      | 31.1 MiB |
| `used_memory_runtime`        | 57.5 MiB |

### Causes

1. **Slab chunks are never returned.** Slabs grow by 1024 vectors per chunk
   (6 MiB at dim 1536). Deleted / expired / evicted records leave free slots that
   only the same collection reuses. Evicting one collection to make room for
   another frees estimated memory but not slab memory.
2. **Chunk granularity:** up to one partly used chunk per collection.
3. **Go GC headroom:** with the default `GOGC=100` the heap may reach ~2× the
   live heap between collections.

### Impact

A server started with `--maxmemory 2gb` can use noticeably more RAM (≈1.5× for
slabs, more for runtime totals). Acceptable for testing / demo workloads; not for
memory-constrained deployments.

### Workarounds today

- Set `GOMEMLIMIT` (standard Go env var) to make the GC stricter.
- Size `--maxmemory` with headroom (e.g. half of the container limit).
- `VDROP` a collection to release its slab entirely.

### Fix options

1. **Return trailing empty chunks**: when the highest chunk has no live slot,
   release it (cheap, partial fix).
2. **Compaction**: move live records into lower slots and release chunks.
   Requires remapping slots in `idToSlot`, parallel arrays, `ttlSlots` and the
   index (HNSW in the future), under the collection write lock or incrementally.
3. **Account allocated memory** (slab bytes) instead of, or in addition to, the
   per-record estimate — only meaningful once (1) or (2) make eviction actually
   free memory.
4. Optionally set `debug.SetMemoryLimit` from `maxmemory` to reduce GC headroom.

### Done when

With `--maxmemory X`, `used_memory_heap_live` stays within a documented bound
of X (e.g. ≤ 1.2·X) in `scripts/e2e/m4_limits.py`.
