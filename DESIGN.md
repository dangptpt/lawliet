# Lawliet — Design Document

> In-memory vector embedding cache, speaking the Redis protocol (RESP).
> Status: **Draft v0** · License: **Apache-2.0** · Language: **Go**

---

## 1. Overview

Lawliet is a lightweight, single-binary, in-memory vector database designed for
**fast testing, prototyping and demoing RAG / LLM applications**.

Heavy vector databases (e.g. Milvus) need object storage, segment management,
lazy loading, etc. Lawliet takes the opposite approach: keep everything in RAM,
speak RESP so any existing Redis client works, and start with one command.

```bash
lawliet --port 6380
```

```python
import redis
r = redis.Redis(port=6380)
r.execute_command("VCREATE", "docs", "DIM", 1536, "METRIC", "cosine")
```

### 1.1 Goals

- Zero-config, single binary, starts in milliseconds.
- RESP2 + RESP3 wire compatibility: usable from `redis-cli`, `redis-py`, `ioredis`, `go-redis`, ... via raw commands,
  with each client's default protocol settings.
- Exact (Flat) vector search with metadata filtering.
- Cache-oriented features: per-record TTL, memory limits, per-collection eviction policies.
- Optional snapshot persistence to a single local file.

### 1.2 Non-goals (MVP)

- Distributed mode, replication, clustering.
- Datasets larger than RAM.
- Object storage, segments, WAL/AOF.
- Authentication / TLS (bind to `127.0.0.1` by default).
- HTTP/REST API (planned later).

---

## 2. Architecture

```
┌──────────────────────────────────────────────┐
│ TCP listener (net)  — 1 goroutine / conn     │
├──────────────────────────────────────────────┤
│ RESP2/RESP3 reader / writer                  │
├──────────────────────────────────────────────┤
│ Command dispatcher  (V*, SAVE, courtesy cmds)│
├──────────────────────────────────────────────┤
│ Store                                        │
│  ├─ Collection (RWMutex each)                │
│  │   ├─ VectorSlab (chunked []float32)       │
│  │   ├─ ID map, text, metadata, TTL, LRU     │
│  │   └─ Index (interface) → FlatIndex        │
│  ├─ MemTracker (estimated bytes)             │
│  └─ Evictor / Expirer                        │
├──────────────────────────────────────────────┤
│ Snapshotter (optional, --dir)                │
└──────────────────────────────────────────────┘
```

### 2.1 Repository layout

```
lawliet/
├── cmd/lawliet/main.go        # flags, wiring, signal handling
├── internal/
│   ├── config/                # flag parsing, size parsing ("2gb")
│   ├── resp/                  # RESP parser + writer (RESP2/RESP3)
│   ├── server/                # TCP server, connection loop, dispatch
│   ├── command/               # one file per command group
│   ├── store/                 # Store, Collection, slab, memtracker, eviction, ttl
│   ├── index/                 # Index interface, flat
│   ├── vecmath/               # dot, l2, normalize
│   ├── filter/                # lexer, parser, evaluator
│   ├── snapshot/              # .lwdb writer / reader
│   └── server/servertest/     # test server + RESP client for integration tests
├── examples/python/           # RAG demo scripts
├── scripts/e2e/               # end-to-end checks against a running server
├── DESIGN.md
├── README.md
└── LICENSE
```

---

## 3. Data Model

```
Server
 └── Collection  (name, dim, metric, index, max_vectors, policy)
      └── Record
           ├── ID        string, user-defined, upsert on duplicate
           ├── Vector    []float32 of length dim
           ├── Text      string (optional, RAG chunk content)
           ├── Metadata  flat typed key/value (optional)
           └── ExpireAt  unix ms, 0 = no TTL
```

No Redis-style numbered databases. `SELECT 0` returns `OK`; other indexes return an error.

### 3.1 Collection config

| Field         | Values                                       | Mutable |
|---------------|----------------------------------------------|---------|
| `name`        | string, unique                               | no      |
| `dim`         | int > 0                                      | no      |
| `metric`      | `cosine` (default) \| `l2` \| `ip`           | no      |
| `index`       | `flat` (MVP)                                 | no (MVP)|
| `max_vectors` | int, 0 = unlimited                           | yes     |
| `policy`      | `noeviction` \| `allkeys-lru` \| `volatile-ttl` (default from server flag) | yes |

Lowering `max_vectors` below the current count **only blocks new inserts**; existing records are kept.

### 3.2 IDs and slots

- External ID: string chosen by the user (e.g. `doc:42`).
- Internal slot: `uint32`, index into the vector slab and parallel arrays.
- Deleted slots become tombstones and are pushed to a free list for reuse.

### 3.3 Vector storage

- Vectors are stored in a **chunked slab**: `[][]float32`, each chunk holds `chunkSize` (1024) vectors.
- Chunks contain no pointers → the Go GC does not scan their contents.
- Growth allocates a new chunk; existing data is never copied.
- For `cosine`, vectors are **L2-normalized on insert**. Returned vectors (`WITHVEC`) are the normalized ones.

### 3.4 Metadata

Passed as a **flat JSON object**. Types are inferred:

| JSON type          | Internal kind | Filter ops                      |
|--------------------|---------------|---------------------------------|
| string             | string        | `== != in`                      |
| integer number     | int64         | `== != < <= > >=`               |
| non-integer number | float64       | `== != < <= > >=`               |
| bool               | bool          | `== !=`                         |
| array of strings   | []string      | `contains`                      |

Nested objects, `null`, arrays of non-strings and empty field names are rejected with
`-ERR invalid metadata`. Integers that do not fit in int64 become float64.
An empty object `{}` is stored as "no metadata".

Metadata is returned as a JSON object string with **sorted keys**. Floats always keep a
decimal point or exponent (`3.0`, not `3`), so they parse back as float.

A stored metadata map is immutable: an update replaces the whole map, so readers can share it
without copying.

### 3.5 Go structures (sketch)

```go
type Collection struct {
    mu  sync.RWMutex
    cfg CollectionConfig

    vectors    VectorSlab
    idToSlot   map[string]uint32
    slotToID   []string
    texts      []string
    meta       []map[string]Value
    expireAt   []int64
    lastAccess []atomic.Int64
    deleted    []bool
    freeList   []uint32
    ttlSlots   map[uint32]struct{} // slots having a TTL, for active expiry / volatile-ttl
    liveCount  int

    index Index
}

type Index interface {
    Add(slot uint32, vec []float32)
    Remove(slot uint32)
    Search(q []float32, k int, accept func(slot uint32) bool) []Hit
}
```

---

## 4. Wire Format

### 4.1 Protocol

RESP2 by default; a connection switches to RESP3 with `HELLO 3` (and back with `HELLO 2`).

Why RESP3 is required: redis-py 8.1.0 defaults to RESP3, sends `HELLO 3` on connect,
does not fall back to RESP2 on `-NOPROTO`, and checks `proto == 3` in the reply
(verified against redis-py 8.1.0 source).

The protocol is stored per connection in the reply writer. Command handlers use
version-neutral writer methods; only these types are encoded differently:

| Type   | RESP2                     | RESP3      |
|--------|---------------------------|------------|
| null   | `$-1` / `*-1`             | `_`        |
| map    | flat array `*2n`          | `%n`       |
| double | bulk string               | `,<float>` |

Not implemented (not needed yet): sets, pushes, attributes, big numbers, verbatim strings,
client-side caching (`CLIENT TRACKING`).

### 4.2 Vector encoding

Two equivalent forms, accepted wherever a vector is expected:

| Form                          | Use                    |
|-------------------------------|------------------------|
| `FP32 <blob>`                 | Code. `dim × 4` bytes, IEEE-754 float32, **little-endian** |
| `VALUES <dim> v1 v2 ... vn`   | Manual testing with `redis-cli` |

Blob length must be a positive multiple of 4 (`-ERR invalid FP32 blob`) and equal `dim × 4`
(`-ERR dimension mismatch`). Values must be finite (no NaN/Inf). With `cosine`, the zero vector
is rejected because it cannot be normalized.

Python client:

```python
blob = emb.astype("<f4").tobytes()   # float32, little-endian
```

### 4.3 Scores

Always **similarity, higher is better**:

| Metric   | Score returned            |
|----------|---------------------------|
| `cosine` | cosine similarity [-1, 1] |
| `ip`     | dot product               |
| `l2`     | `-euclidean_distance`     |

Scores are written with `WriteDouble`: RESP3 double, or bulk string in RESP2.

---

## 5. Commands

### 5.1 Collections

```
VCREATE <col> DIM <n> [METRIC cosine|l2|ip] [INDEX flat]
        [MAX_VECTORS <n>] [POLICY noeviction|allkeys-lru|volatile-ttl]   → +OK
VDROP   <col>                                                           → :1 | :0
VLIST                                                                   → array of names
VINFO   <col>                                                           → flat key/value array
VCONFIG <col> GET                                                       → flat key/value array
VCONFIG <col> SET POLICY <p>                                            → +OK
VCONFIG <col> SET MAX_VECTORS <n>                                       → +OK
```

`VINFO` reply is a map (flat array in RESP2). Fields: `name, dim, metric, index, count, expires,
max_vectors, policy, memory_bytes` (estimated, §8.2), `vector_bytes` (allocated slab).

`VCONFIG <col> SET` accepts several `param value` pairs; all are validated before any change is
applied. Unknown or immutable parameters (`DIM`, `METRIC`, `INDEX`) are rejected.

### 5.2 Records

```
VADD <col> <id> (FP32 <blob> | VALUES <dim> v...)
     [TEXT <str>] [META <json>] [EX <sec> | PX <ms>] [NX | XX]   → :1 inserted | :0 updated | nil (NX/XX not met)
VGET    <col> <id> [WITHVEC]       → [text, meta_json, (vector blob)] | nil   (text/meta are null until M2)
VDEL    <col> <id> [<id> ...]      → :<deleted count>
VEXISTS <col> <id>                 → :1 | :0
VCOUNT  <col>                      → :<n>
```

- No flag: upsert. `NX`: only insert if absent. `XX`: only update if present.
- An update **replaces the whole record**: `TEXT`/`META` not given are cleared, and an update
  without `EX/PX` removes an existing TTL (same as Redis `SET`).
- In `VGET`/`VSEARCH` replies, empty text and missing metadata are null.

### 5.3 TTL

```
VEXPIRE  <col> <id> <sec>   → :1 | :0
VTTL     <col> <id>         → seconds | -1 (no TTL) | -2 (not found)
VPERSIST <col> <id>         → :1 | :0
```

- `EX`/`PX` on `VADD` must be positive; TTLs are capped at ~100 years (`-ERR invalid expire time`).
  `EX` and `PX` together → syntax error.
- `VEXPIRE` with a value ≤ 0 deletes the record immediately and returns 1 (same as Redis `EXPIRE`).
- `VTTL` rounds the remaining time to the nearest second, like Redis `TTL`.
- `VPERSIST` returns 0 if the record has no TTL.
- An expired record behaves as missing for every command (`NX` inserts, `XX` skips,
  `VDEL` counts 0, `VEXPIRE`/`VPERSIST` return 0).

### 5.4 Search

```
VSEARCH <col> (FP32 <blob> | VALUES <dim> v...)
        [K <n>] [FILTER <expr>] [WITHTEXT] [WITHMETA] [WITHVEC]
```

`K` defaults to 10, range 1–10000. Reply: array of hits, best first. Each hit is an array:

```
[id, score, (text), (meta_json), (vector blob)]
```

Optional parts always appear in this fixed order, whatever the order of the flags.
`FILTER` is applied **before** ranking: the reply holds the best K records among those
matching the filter (fewer if fewer match).

Records returned in the top-K have their `lastAccess` updated.

### 5.5 Persistence

```
SAVE   → +OK | -ERR snapshot disabled: start server with --dir
```

Synchronous. Serialized with automatic snapshots via a mutex.

### 5.6 Courtesy commands (client compatibility)

| Command                           | Behavior                               |
|-----------------------------------|----------------------------------------|
| `PING [msg]`                      | `+PONG` or echo                        |
| `ECHO msg`                        | echo                                   |
| `HELLO [2\|3] [SETNAME n]`        | switch protocol; map with server, version, proto, id, mode, role, modules; other versions → `-NOPROTO` |
| `SELECT 0`                        | `+OK`; other → error                   |
| `CLIENT SETNAME/SETINFO/GETNAME`  | `+OK` / name                           |
| `COMMAND`, `COMMAND DOCS`         | empty array                            |
| `INFO [section]`                  | `server`, `memory`, `stats`, `persistence` |
| `QUIT`                            | `+OK`, close connection                |

---

## 6. Filter Language

### 6.1 Grammar (EBNF)

```
expr       = or_expr ;
or_expr    = and_expr { "or" and_expr } ;
and_expr   = not_expr { "and" not_expr } ;
not_expr   = [ "not" ] primary ;
primary    = "(" expr ")" | comparison ;
comparison = IDENT op literal
           | IDENT "in" "[" literal { "," literal } "]"
           | IDENT "contains" STRING ;
op         = "==" | "!=" | "<" | "<=" | ">" | ">=" ;
literal    = STRING | NUMBER | "true" | "false" ;
STRING     = '"' { char } '"' ;
```

Lexical rules:

- Keywords (`and or not in contains true false`) are case-insensitive.
- `IDENT` = `[A-Za-z_][A-Za-z0-9_.]*` (e.g. `user.id`). Field names with other characters
  cannot be filtered on yet.
- Strings use double or single quotes, so filters are easy to pass from a shell:
  `FILTER 'source == "hr.md"'`. Escapes: `\" \' \\ \n \t \r`.
- `NUMBER` = `[+-]digits[.digits][e[+-]digits]`. Without `.`/`e` it is an int64, else a float64.
- `not` may be repeated (`not not x == 1`).

Precedence: `not` > `and` > `or`.

Limits: 16 KiB per expression, nesting depth 64, 1024 values per `in` list.

### 6.2 Semantics

- Missing field → comparison is `false`, for `!=` too (so `not field == x` is `true`,
  but `field != x` is `false` when the field is missing).
- Type mismatch at evaluation (e.g. `page == "3"` when `page` is int) → `false`, not an error.
- Type errors visible at parse time are errors: `<`, `<=`, `>`, `>=` need a number literal.
- int and float compare numerically (`page == 3.0` matches `3`).
- `contains` only matches string-list fields; `in` only matches scalar fields.
- Parse errors report the byte position: `-ERR invalid filter: <reason> at position <n>`.
- Parsed once per `VSEARCH`, evaluated per record (brute force) **before** distance computation.

Example:

```
(source == "hr.md" or source in ["it.md", "ops.md"]) and page >= 2 and not tags contains "draft"
```

---

## 7. Search (Flat Index)

- Iterate all live, non-expired slots; apply filter; compute score.
- Keep top-K with a **min-heap of size K**.
- Cosine on normalized vectors = dot product.
- Later optimization: split slab across goroutines, merge per-worker heaps; loop unrolling / SIMD.

---

## 8. Memory Limits & Eviction

### 8.1 Limits

| Limit            | Scope      | Flag / option               |
|------------------|------------|-----------------------------|
| `maxmemory`      | server     | `--maxmemory 2gb` (0 = off) |
| `max-collections`| server     | `--max-collections <n>`     |
| `max_vectors`    | collection | `VCREATE ... MAX_VECTORS n` |

### 8.2 Memory accounting

Memory is **estimated**, not measured: each insert/delete adds/subtracts

```
dim*4 + len(id)*2 + len(text) + metadata (48 + Σ(88 + len(key) + len(value))) + 96 overhead
```

Upsert reserves only the delta.

**`maxmemory` limits the logical size of the records, not the process memory.**
Measured with dim 1536 and `--maxmemory 20mb` (scripts/e2e/m4_limits.py):

| Figure (`INFO memory`)       | Value    | What it is                                   |
|------------------------------|----------|----------------------------------------------|
| `used_memory_estimated`      | 20.0 MiB | the limited figure                           |
| `used_memory_vector_slab`    | 30.0 MiB | allocated slab chunks                        |
| `used_memory_heap_live`      | 31.1 MiB | Go heap in use after the last GC             |
| `used_memory_runtime`        | 57.5 MiB | all memory mapped by the Go runtime          |

Sources of the difference:

1. **Chunk granularity**: slabs grow by 1024 vectors (6 MiB at dim 1536) per collection.
2. **Freed slots stay allocated**: deleted, expired and evicted records leave slots that only
   the same collection reuses; chunks are never returned. Evicting a cache to make room for
   another collection frees *estimated* memory but not slab memory.
3. **Go GC headroom**: by default the heap may grow to about 2× the live heap between GCs.
   Users can set the standard `GOMEMLIMIT` environment variable to make the GC stricter.

Tracked as **TD-1** in [TECH_DEBT.md](TECH_DEBT.md): return empty chunks / compact slabs, then
optionally account allocated memory.

### 8.3 Policies (per collection)

| Policy          | Victim                                   |
|-----------------|------------------------------------------|
| `noeviction`    | none → error                             |
| `allkeys-lru`   | lowest `lastAccess` among N random samples |
| `volatile-ttl`  | lowest `expireAt` among N random samples with TTL |

`N = 5`. Approximate (sampling) by design, similar to Redis. Collections with ≤ 5 records
consider every record (exact). Expired records in a sample are always chosen first.
With 2000 records and 10% hot records, sampled LRU kept 198–200 of 200 hot records in tests.

"Access" = `VADD`, `VGET`, or being returned in `VSEARCH` top-K.

### 8.4 Flow on insert of a new record

```
VADD
 1. need = size(new record) − size(existing record, if any)
 2. Store.freeMemory(need)          (no collection lock held)
      need > maxmemory              → -OOM (nothing is evicted)
      while used + need > maxmemory:
        a. reclaim expired records in any collection (no data loss)
        b. pick a random collection whose policy != noeviction, evict one record
        nothing left to reclaim or evict → -OOM
 3. lock collection
 4. new record and count >= max_vectors:
        reclaim one expired record (sampled), else evict by policy,
        else -ERR max_vectors reached
 5. insert, unlock
```

Never call `freeMemory` while holding a collection lock (deadlock risk).
Another writer may use freed memory between steps 2 and 5, so the limit can be exceeded by a
few records under concurrency. Deletes, reads, `VEXPIRE`, `VPERSIST` and `VCONFIG` are always
allowed at the limit.

`INFO` also reports `maxmemory`, `maxmemory_policy` (default for new collections),
`evicted_keys` (stats) and a `keyspace` section with `collections` and `max_collections`.

---

## 9. TTL Expiry

TTLs are stored as absolute unix milliseconds per slot (`expireAt`); `ttlSlots` holds the
slots that have one.

- **Visibility**: a record is expired as soon as `now >= expireAt`. Every read (`VGET`,
  `VEXISTS`, `VTTL`, `VSEARCH`) and write treats it as missing immediately.
- **Lazy reclaim**: reads hold only the read lock, so they note expired records and delete
  them right after releasing it (re-checking expiry under the write lock).
- **Active reclaim**: a background goroutine runs every 100 ms. For each collection it
  samples up to 20 entries of `ttlSlots` (Go map iteration order is randomized, which gives
  a cheap sample), deletes the expired ones, and repeats while > 25% of the sample was
  expired. Each cycle may use at most 25 ms. A small residue of expired records can remain
  after one cycle; later cycles or reads reclaim it.
- **Counts**: `VCOUNT` / `VINFO count` include expired records not yet reclaimed, like
  Redis `DBSIZE`. The active cycle keeps the difference small.
- The clock is injectable (`Store.SetClock`) so tests do not sleep.

---

## 10. Persistence (Snapshot)

Disabled unless `--dir` is given. `--dir` must be an existing directory; `--dbfilename`
(default `dump.lwdb`) must be a plain file name.

### 10.1 Triggers

- Save rules: `--save "<seconds> <changes>"` (repeatable). Defaults when `--dir` is set:
  `3600 1`, `300 100`, `60 10000`. `--save ""` disables auto-save. Rules are checked every
  second; after a failed save the next attempt waits at least 5 s.
- `SAVE` command (synchronous; one save at a time).
- Graceful shutdown (`SIGINT`, `SIGTERM`): the server first stops accepting and closes all
  connections, **then** saves, so no write can land after the final snapshot.
- Load on startup if the file exists, before the server listens. Limits (`maxmemory`,
  `max_vectors`, `max-collections`) are not enforced while loading, like Redis loading an RDB.
  Records that expired while the server was down are skipped.
- A file that fails to load (bad magic, version, checksum, truncation) stops the server with
  exit code 1 and a log message; it is never partially loaded into a running server.

`dirty` counter increments on every mutation; after a successful snapshot it is decreased
by the value captured at snapshot start (mutations during the snapshot are kept).

### 10.2 Consistency

Collections are snapshotted one at a time, each under its **read lock**:
reads continue, writes to that collection wait. (Go cannot safely `fork()` like Redis.)
Each collection is consistent; the snapshot is not atomic across collections.

Measured: 10 000 records × 384 dims with text and metadata → 15.4 MiB written in ~60 ms.

### 10.3 Safe write

```
write  <dir>/dump.lwdb.tmp
fsync
rename dump.lwdb.tmp → dump.lwdb   (atomic on POSIX, same filesystem)
```

### 10.4 File format `.lwdb` (little-endian)

```
magic        "LWDB"           4B
version      uint16
n_coll       uint32
repeat n_coll:
  name       str
  dim        uint32
  metric     uint8
  index      uint8
  max_vec    uint32
  policy     uint8
  n_rec      uint32
  repeat n_rec:
    id       str
    vector   dim × float32
    text     str
    meta     str (JSON)
    expireAt int64
crc32        uint32 (IEEE, over everything above)

str = uint32 length + bytes
```

Tombstones and expired records are skipped (natural compaction).
`lastAccess` is not persisted; set to load time.
Vectors are written in their stored form and restored **bit-exact**: cosine vectors are not
normalized again on load (re-normalizing changes the float bits of ~50% of vectors and thus
the scores).
Bad magic / version / CRC → refuse to start with a clear error.

`INFO persistence`: `snapshot_enabled`, `snapshot_path`, `rdb_changes_since_last_save`,
`rdb_last_save_time`, `rdb_last_save_status`, `rdb_last_save_duration_ms`.

---

## 11. Concurrency

- One goroutine per connection.
- `Store.mu` (RWMutex) guards the collection map.
- `Collection.mu` (RWMutex): `VSEARCH`/`VGET` take read lock; mutations take write lock.
- `lastAccess` uses atomics so readers can update it under the read lock.
- `MemTracker` uses atomic CAS.
- Lock order: `Store.mu` → `Collection.mu`. Never hold two collection locks at once.

---

## 12. Server Flags

| Flag                  | Default        |
|-----------------------|----------------|
| `--bind`              | `127.0.0.1`    |
| `--port`              | `6380`         |
| `--maxmemory`         | `0` (unlimited)|
| `--maxmemory-policy`  | `noeviction` (default for new collections) |
| `--max-collections`   | `0` (unlimited)|
| `--dir`               | empty → persistence off |
| `--dbfilename`        | `dump.lwdb`    |
| `--save`              | see §10.1      |

---

## 13. Errors

| Error                                                        | When                          |
|--------------------------------------------------------------|-------------------------------|
| `-ERR unknown command '<cmd>'`                               | unknown command               |
| `-ERR wrong number of arguments for '<cmd>' command`         | arity                         |
| `-ERR syntax error`                                          | bad option                    |
| `-ERR collection not found`                                  | missing collection            |
| `-ERR collection already exists`                             | duplicate `VCREATE`           |
| `-ERR max collections reached`                               | `max-collections` hit         |
| `-ERR dimension mismatch: expected <d>, got <n>`             | wrong vector length           |
| `-ERR invalid FP32 blob: length <n> is not a positive multiple of 4` | malformed blob        |
| `-ERR VALUES <n> expects <n> numbers, got <m>`               | too few VALUES                |
| `-ERR invalid number in VALUES: '<x>'`                       | non-numeric value             |
| `-ERR zero vector is not allowed with cosine metric`         | zero vector, cosine           |
| `-ERR vector contains NaN or Inf`                            | non-finite value              |
| `-ERR K must be an integer between 1 and 10000`              | bad `K`                       |
| `-ERR invalid metadata: <reason>`                            | bad `META` JSON               |
| `-ERR invalid expire time`                                   | bad `EX`/`PX`/`VEXPIRE` value |
| `-ERR invalid filter: <reason> at position <n>`              | bad `FILTER` expression       |
| `-ERR max_vectors reached`                                   | limit hit, nothing evictable  |
| `-OOM command not allowed when used memory > 'maxmemory'`    | memory limit, cannot evict    |
| `-ERR snapshot disabled: start server with --dir`            | `SAVE` without `--dir`        |
| `-ERR snapshot failed: <reason>`                             | `SAVE` could not write        |
| `-NOPROTO unsupported protocol version`                      | `HELLO` with version other than 2/3 |

---

## 14. Roadmap

### MVP

| Milestone | Scope | Done when |
|-----------|-------|-----------|
| **M0** Skeleton ✅ | RESP2/RESP3 parser/writer, TCP server, courtesy commands | `redis-cli -p 6380 ping` → `PONG`; `redis-py` connects with default settings |
| **M1** Core vectors ✅ | Collections, slab, `VADD/VGET/VDEL/VEXISTS/VCOUNT`, `VALUES`/`FP32`, Flat `VSEARCH` (no filter) | Python script inserts 10k vectors and gets correct top-K vs NumPy |
| **M2** Metadata & filter ✅ | `TEXT`, `META`, filter lexer/parser/evaluator, `WITH*` flags | Filter unit tests incl. `and/or/not/()`, `in`, `contains` |
| **M3** TTL ✅ | `EX/PX`, `VEXPIRE/VTTL/VPERSIST`, lazy + active expiry | Records disappear after TTL; `VTTL` matches Redis semantics |
| **M4** Limits & eviction ✅ | MemTracker, `maxmemory`, `max_vectors`, 3 policies, `VCONFIG`, `VINFO`, `INFO` | Eviction tests per policy; `noeviction` returns errors |
| **M5** Snapshot ✅ | `.lwdb` writer/reader, save rules, `SAVE`, shutdown save, startup load | Restart restores identical search results; corrupt file detected |
| **M6** Release | README quickstart, Python RAG example, Dockerfile, GitHub Actions CI, benchmark script | `docker run` + example works end-to-end |

### Phase 2 (after MVP)

- HNSW index (behind `Index` interface)
- HTTP/REST API
- Batch insert command
- Metadata secondary index for faster filtering
- Quantization (int8 / binary), float16
- Parallel Flat search, SIMD distance
- AUTH / TLS
- LangChain & LlamaIndex adapters
- Chunked/incremental snapshot to reduce write blocking
- Return empty slab chunks / compact slabs so evicted memory is really freed (§8.2, TD-1)

---

## 15. Project Info

- Go module path: `github.com/tranbaohieu/lawliet`
- Default port: `6380`
