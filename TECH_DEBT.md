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

---

## TD-2: IVF / PQ training blocks a write

- **Recorded:** 2026-11-16 (ANN spike)
- **Status:** open
- **Design reference:** DESIGN.md §7.4, §16.3

### Problem

IVF trains (k-means, PQ codebooks) inside the `VADD` that crosses a training threshold, under the
collection write lock. Measured at 100k × 1024 (`docs/benchmarks/ann-2026-11-16`): the slowest
single insert takes 3.8 s (IVF-Flat), 5.3 s (IVF-SQ8), 12.6 s (IVF-PQ). Searches on that
collection wait as well. With the AVX2 kernels: 0.9 s, 1.4 s, 9.6 s (PQ training runs on
16-dim sub-vectors, which stay on the scalar path).

### Fix options

1. Train on a snapshot of the sample in a background goroutine; keep serving with the old
   centroids (or exact search) and swap under a short write lock, re-assigning records added
   meanwhile.
2. Mini-batch / e-means updates (DESIGN §16.3) to make retraining incremental.
3. Parallelize Lloyd iterations further (assignment already is) and use SIMD dot products.

### Done when

No single `VADD` exceeds 50 ms while an IVF collection grows from 0 to 100k records.

---

## TD-3: HNSW quality degrades under churn

- **Recorded:** 2026-11-16 (ANN spike)
- **Status:** partly fixed 2026-11-16 — deletes repair neighbors (fix option 1): after 3 × 30%
  churn recall@10 is 0.971 instead of 0.954 (fresh build 0.979). Not yet within the 0.005 target;
  the repaired graph has 8% fewer links. Next: refill pruned links up to M (keep pruned
  connections) or option 2.
- **Design reference:** DESIGN.md §7.3, §16.3

### Problem

Deletes are tombstones and a reused slot is re-inserted with new links, but links from other
nodes to the old vector stay. After deleting and re-inserting 30% of records three times,
recall@10 at ef=64 falls 0.979 → 0.954 and the graph loses 8% of its links
(`docs/benchmarks/ann-2026-11-16`). TTL expiry and LRU eviction in a semantic cache cause
exactly this pattern.

### Fix options

1. On delete, repair neighbors of the removed node (connect them to each other with the
   heuristic), as hnswlib's `markDelete` + replace does.
2. Rebuild the graph in the background when the share of replaced nodes since the last build
   exceeds a threshold (e.g. 30%), swapping atomically.

### Done when

Recall after 3 × 30% churn stays within 0.005 of the fresh build in the benchmark.
