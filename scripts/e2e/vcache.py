"""VCACHE end-to-end check: semantic cache commands on a real server, and cache
defaults surviving a restart.

Starts its own server (port 6394) in a temporary --dir.

Usage:
    pip install redis numpy
    go build -o bin/lawliet ./cmd/lawliet
    python scripts/e2e/vcache.py [--bin ./bin/lawliet]
"""

import argparse
import os
import signal
import subprocess
import sys
import tempfile
import time

import numpy as np
import redis

PORT = 6394
DIM = 384


def check(cond, msg):
    print(("OK   " if cond else "FAIL ") + msg)
    return cond


def start(binary, workdir):
    proc = subprocess.Popen([binary, "--port", str(PORT), "--dir", workdir, "--save", ""],
                            stderr=subprocess.DEVNULL)
    r = redis.Redis(port=PORT)
    for _ in range(200):
        try:
            r.ping()
            return proc, r
        except redis.ConnectionError:
            time.sleep(0.05)
    raise RuntimeError("server did not start")


def as_dict(v):
    return v if isinstance(v, dict) else dict(zip(v[::2], v[1::2]))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default="./bin/lawliet")
    args = ap.parse_args()
    binary = os.path.abspath(args.bin)
    workdir = tempfile.mkdtemp(prefix="lawliet-vcache-")
    rng = np.random.default_rng(11)
    ok = True

    proc, r = start(binary, workdir)
    r.execute_command("VCREATE", "answers", "DIM", DIM, "POLICY", "allkeys-lru",
                      "MAX_VECTORS", 1000, "CACHE_THRESHOLD", "0.95", "CACHE_TTL", 3600)

    # 200 cached questions; queries are noisy copies (paraphrases) or unrelated vectors.
    base = rng.standard_normal((200, DIM)).astype("<f4")
    pipe = r.pipeline(transaction=False)
    for i, v in enumerate(base):
        pipe.execute_command("VCACHE.SET", "answers", f"q:{i}", "FP32", v.tobytes(),
                             "TEXT", f"answer {i}", "META", f'{{"model":"m{i % 2}"}}')
    pipe.execute()
    ttl = r.execute_command("VTTL", "answers", "q:0")
    ok &= check(3590 <= ttl <= 3600, f"CACHE_TTL applied by VCACHE.SET ({ttl}s)")

    hits = wrong = 0
    lat = []
    for i in range(100):
        q = (base[i] + 0.05 * rng.standard_normal(DIM)).astype("<f4")  # paraphrase: cos ≈ 0.999
        t = time.perf_counter()
        res = r.execute_command("VCACHE.GET", "answers", "FP32", q.tobytes())
        lat.append((time.perf_counter() - t) * 1000)
        if res is not None:
            hits += 1
            wrong += res[0] != f"q:{i}".encode()
    misses = 0
    for _ in range(100):
        q = rng.standard_normal(DIM).astype("<f4")  # unrelated: cos ≈ 0
        misses += r.execute_command("VCACHE.GET", "answers", "FP32", q.tobytes()) is None
    ok &= check(hits == 100 and wrong == 0, f"paraphrased questions hit the right entry ({hits}/100, {wrong} wrong)")
    ok &= check(misses == 100, f"unrelated questions miss ({misses}/100)")
    print(f"     VCACHE.GET p50 {np.median(lat):.2f} ms over 200 entries")

    st = as_dict(r.execute_command("VCACHE.STATS", "answers"))
    ok &= check(st[b"hits"] == 100 and st[b"misses"] == 100 and abs(float(st[b"hit_rate"]) - 0.5) < 1e-9,
                f"VCACHE.STATS: hits={st[b'hits']} misses={st[b'misses']} hit_rate={float(st[b'hit_rate']):.2f} "
                f"avg_hit_score={float(st[b'avg_hit_score']):.4f}")

    q0 = (base[0] + 0.05 * rng.standard_normal(DIM)).astype("<f4").tobytes()
    ok &= check(r.execute_command("VCACHE.GET", "answers", "FP32", q0, "FILTER", 'model == "m1"') is None,
                "FILTER scopes lookups (q:0 has model m0)")
    removed = r.execute_command("VCACHE.INVALIDATE", "answers", "FILTER", 'model == "m0"')
    ok &= check(removed == 100 and r.execute_command("VCOUNT", "answers") == 100,
                f"VCACHE.INVALIDATE removed {removed} entries of model m0")

    # Cache defaults persist across restarts; stats are in-memory only.
    r.execute_command("SAVE")
    proc.send_signal(signal.SIGTERM)
    proc.wait(timeout=30)
    proc, r = start(binary, workdir)
    cfg = as_dict(r.execute_command("VCONFIG", "answers", "GET"))
    ok &= check(float(cfg[b"cache_threshold"]) == 0.95 and cfg[b"cache_ttl"] == 3600,
                "CACHE_THRESHOLD and CACHE_TTL restored after restart")
    st = as_dict(r.execute_command("VCACHE.STATS", "answers"))
    ok &= check(st[b"hits"] == 0 and st[b"entries"] == 100, "stats reset after restart, entries kept")
    q1 = (base[1] + 0.05 * rng.standard_normal(DIM)).astype("<f4").tobytes()
    res = r.execute_command("VCACHE.GET", "answers", "FP32", q1)
    ok &= check(res is not None and res[2] == b"answer 1", "cache still answers after restart")

    proc.send_signal(signal.SIGTERM)
    proc.wait(timeout=30)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
