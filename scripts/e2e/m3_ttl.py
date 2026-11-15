"""M3 end-to-end check: TTL with the real clock and the background expirer.

Usage:
    pip install redis numpy
    ./bin/lawliet &
    python scripts/e2e/m3_ttl.py [--port 6380]
"""

import argparse
import sys
import time

import numpy as np
import redis


def vinfo(r, col):
    """VINFO as a dict: a map in RESP3, a flat key/value list in RESP2."""
    v = r.execute_command("VINFO", col)
    return v if isinstance(v, dict) else dict(zip(v[::2], v[1::2]))


def check(cond, msg):
    print(("OK   " if cond else "FAIL ") + msg)
    return cond


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=6380)
    args = ap.parse_args()

    r = redis.Redis(port=args.port)
    col, dim = "e2e_ttl", 64
    r.execute_command("VDROP", col)
    r.execute_command("VCREATE", col, "DIM", dim)
    rng = np.random.default_rng(1)

    n_short, n_keep = 5000, 500
    pipe = r.pipeline(transaction=False)
    for i in range(n_short):
        pipe.execute_command("VADD", col, f"cache:{i}", "FP32",
                             rng.standard_normal(dim).astype("<f4").tobytes(), "PX", 1000)
    for i in range(n_keep):
        pipe.execute_command("VADD", col, f"doc:{i}", "FP32",
                             rng.standard_normal(dim).astype("<f4").tobytes())
    pipe.execute()

    ok = True
    ok &= check(r.execute_command("VCOUNT", col) == n_short + n_keep, "all records counted before expiry")
    ttl = r.execute_command("VTTL", col, "cache:0")
    ok &= check(ttl in (0, 1), f"VTTL of 1000ms record rounds to 0 or 1 (got {ttl})")
    ok &= check(r.execute_command("VTTL", col, "doc:0") == -1, "VTTL -1 for record without TTL")
    ok &= check(vinfo(r, col)[b"expires"] == n_short, "VINFO expires counts TTL records")

    time.sleep(1.1)  # records expired, but nothing has touched them yet

    q = rng.standard_normal(dim).astype("<f4").tobytes()
    hits = r.execute_command("VSEARCH", col, "FP32", q, "K", 100)
    ok &= check(all(h[0].startswith(b"doc:") for h in hits) and len(hits) == 100,
                "VSEARCH never returns expired records")
    ok &= check(r.execute_command("VEXISTS", col, "cache:1") == 0, "VEXISTS 0 after expiry")
    ok &= check(r.execute_command("VTTL", col, "cache:2") == -2, "VTTL -2 after expiry")

    # Active expiry: the count must drop to n_keep without further reads.
    # (The search above reclaimed what it scanned; add fresh short-lived records.)
    pipe = r.pipeline(transaction=False)
    for i in range(n_short):
        pipe.execute_command("VADD", col, f"cache2:{i}", "FP32",
                             rng.standard_normal(dim).astype("<f4").tobytes(), "PX", 200)
    pipe.execute()
    t0 = time.perf_counter()
    count = None
    while time.perf_counter() - t0 < 5:
        count = r.execute_command("VCOUNT", col)
        if count == n_keep:
            break
        time.sleep(0.05)
    elapsed = time.perf_counter() - t0
    ok &= check(count == n_keep, f"background expirer reclaimed {n_short} records in {elapsed:.2f}s (count={count})")

    r.execute_command("VADD", col, "x", "FP32", q, "EX", 100)
    r.execute_command("VPERSIST", col, "x")
    ok &= check(r.execute_command("VTTL", col, "x") == -1, "VPERSIST removes TTL")
    ok &= check(r.execute_command("VEXPIRE", col, "x", 0) == 1 and r.execute_command("VEXISTS", col, "x") == 0,
                "VEXPIRE 0 deletes immediately")

    r.execute_command("VDROP", col)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
