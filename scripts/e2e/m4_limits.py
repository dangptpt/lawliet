"""M4 end-to-end check: maxmemory, per-collection eviction policies, max_vectors.

Starts its own server with --maxmemory (does not use a running one).

Usage:
    pip install redis numpy
    go build -o bin/lawliet ./cmd/lawliet
    python scripts/e2e/m4_limits.py [--bin ./bin/lawliet] [--port 6391]
"""

import argparse
import subprocess
import sys
import time

import numpy as np
import redis

DIM = 1536  # OpenAI-size embeddings: ~6.2 KB per record


def check(cond, msg):
    print(("OK   " if cond else "FAIL ") + msg)
    return cond


def info(r, section):
    return r.info(section)  # redis-py parses INFO into a dict


def vinfo(r, col):
    v = r.execute_command("VINFO", col)
    return v if isinstance(v, dict) else dict(zip(v[::2], v[1::2]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default="./bin/lawliet")
    ap.add_argument("--port", type=int, default=6391)
    args = ap.parse_args()

    proc = subprocess.Popen([args.bin, "--port", str(args.port), "--maxmemory", "20mb"],
                            stderr=subprocess.DEVNULL)
    try:
        r = redis.Redis(port=args.port)
        for _ in range(100):
            try:
                r.ping()
                break
            except redis.ConnectionError:
                time.sleep(0.05)
        sys.exit(0 if run(r) else 1)
    finally:
        proc.terminate()
        proc.wait()


def run(r):
    rng = np.random.default_rng(3)
    vec = lambda: rng.standard_normal(DIM).astype("<f4").tobytes()
    limit = 20 << 20
    ok = True

    r.execute_command("VCREATE", "docs", "DIM", DIM)  # noeviction (server default)
    r.execute_command("VCREATE", "cache", "DIM", DIM, "POLICY", "allkeys-lru")

    # 1. Fill the semantic cache.
    pipe = r.pipeline(transaction=False)
    for i in range(2500):
        pipe.execute_command("VADD", "cache", f"q:{i}", "FP32", vec(), "TEXT", f"answer {i}")
    pipe.execute()
    cache_full = r.execute_command("VCOUNT", "cache")
    ok &= check(cache_full == 2500, f"cache filled: {cache_full} records")

    # 2. Keep 100 cache entries hot (recently read).
    for i in range(100):
        r.execute_command("VGET", "cache", f"q:{i}")

    # 3. Documents push the cache out; documents are never evicted.
    pipe = r.pipeline(transaction=False)
    for i in range(1500):
        pipe.execute_command("VADD", "docs", f"doc:{i}", "FP32", vec(), "TEXT", f"chunk {i}")
    res = pipe.execute(raise_on_error=False)
    errors = [e for e in res if isinstance(e, Exception)]
    ok &= check(not errors, f"1500 documents inserted without error ({len(errors)} errors)")
    ok &= check(r.execute_command("VCOUNT", "docs") == 1500, "all documents kept")

    mem = info(r, "memory")
    used = int(mem["used_memory_estimated"])
    evicted = int(info(r, "stats")["evicted_keys"])
    cache_now = r.execute_command("VCOUNT", "cache")
    ok &= check(used <= limit, f"used_memory_estimated {used / 2**20:.1f} MiB <= maxmemory 20 MiB")
    ok &= check(evicted > 0 and cache_now == cache_full - evicted,
                f"cache evicted {evicted} records, {cache_now} left")
    hot = sum(r.execute_command("VEXISTS", "cache", f"q:{i}") for i in range(100))
    ok &= check(hot >= 95, f"approximate LRU kept {hot}/100 hot cache entries")
    mib = lambda k: int(mem[k]) / 2**20
    print(f"     estimated {mib('used_memory_estimated'):.1f} MiB | vector slab "
          f"{mib('used_memory_vector_slab'):.1f} MiB | live heap {mib('used_memory_heap_live'):.1f} MiB | "
          f"runtime total {mib('used_memory_runtime'):.1f} MiB")

    # 4. Once the cache is empty, documents get OOM.
    # redis-py maps the "-OOM" error code to OutOfMemoryError (and strips the code).
    oom = None
    for i in range(1500, 5000):
        try:
            r.execute_command("VADD", "docs", f"doc:{i}", "FP32", vec())
        except redis.ResponseError as e:
            oom = e
            break
    ok &= check(isinstance(oom, redis.exceptions.OutOfMemoryError),
                f"OOM once nothing is evictable: {type(oom).__name__}: {oom}")
    ok &= check(r.execute_command("VCOUNT", "cache") == 0, "cache fully evicted before OOM")
    ok &= check(len(r.execute_command("VSEARCH", "docs", "FP32", vec(), "K", 5)) == 5,
                "reads still work at the memory limit")
    ok &= check(r.execute_command("VDEL", "docs", "doc:0") == 1, "deletes still work at the memory limit")

    # 5. max_vectors with volatile-ttl.
    r.execute_command("VCREATE", "sess", "DIM", 4, "MAX_VECTORS", 3, "POLICY", "volatile-ttl")
    small = lambda: rng.standard_normal(4).astype("<f4").tobytes()
    r.execute_command("VADD", "sess", "s1", "FP32", small(), "EX", 10)
    r.execute_command("VADD", "sess", "s2", "FP32", small(), "EX", 1000)
    r.execute_command("VADD", "sess", "pinned", "FP32", small())
    r.execute_command("VADD", "sess", "s3", "FP32", small(), "EX", 500)
    ok &= check(r.execute_command("VEXISTS", "sess", "s1") == 0 and
                r.execute_command("VEXISTS", "sess", "pinned") == 1,
                "volatile-ttl evicted the record expiring soonest, kept the one without TTL")
    v = vinfo(r, "sess")
    ok &= check(v[b"max_vectors"] == 3 and v[b"policy"] == b"volatile-ttl" and v[b"count"] == 3,
                "VINFO shows max_vectors, policy and count")
    return ok


if __name__ == "__main__":
    main()
