"""End-to-end benchmark: insert throughput and search latency over RESP.

Starts its own server (port 6393), so numbers include the network round trip
and client-side encoding, like a real application.

Usage:
    pip install redis numpy
    go build -o bin/lawliet ./cmd/lawliet
    python scripts/bench/bench.py [--sizes 10000,50000] [--dims 384,1536] [--threads 8]
"""

import argparse
import json
import os
import platform
import subprocess
import threading
import time

import numpy as np
import redis

PORT = 6393


def start_server(binary):
    proc = subprocess.Popen([binary, "--port", str(PORT)], stderr=subprocess.DEVNULL)
    r = redis.Redis(port=PORT)
    for _ in range(200):
        try:
            r.ping()
            return proc
        except redis.ConnectionError:
            time.sleep(0.05)
    raise RuntimeError("server did not start")


def pct(values, p):
    return float(np.percentile(values, p))


def bench_case(n, dim, queries, threads, rng):
    r = redis.Redis(port=PORT)
    col = f"bench_{n}_{dim}"
    r.execute_command("VDROP", col)
    r.execute_command("VCREATE", col, "DIM", dim)
    data = rng.standard_normal((n, dim)).astype("<f4")

    t0 = time.perf_counter()
    pipe = r.pipeline(transaction=False)
    for i in range(n):
        pipe.execute_command("VADD", col, f"doc:{i}", "FP32", data[i].tobytes(),
                             "META", json.dumps({"cat": i % 10}))
        if (i + 1) % 1000 == 0:
            pipe.execute()
    pipe.execute()
    insert_rate = n / (time.perf_counter() - t0)

    qs = [rng.standard_normal(dim).astype("<f4").tobytes() for _ in range(queries)]

    def latencies(extra):
        out = []
        for q in qs:
            t = time.perf_counter()
            r.execute_command("VSEARCH", col, "FP32", q, "K", 10, *extra)
            out.append((time.perf_counter() - t) * 1000)
        return out

    latencies([])  # warm up
    plain = latencies([])
    filtered = latencies(["FILTER", "cat == 3"])  # 10% of records match

    # Throughput with concurrent clients (one connection per thread).
    per_thread = max(1, queries // threads)
    done = []

    def worker():
        c = redis.Redis(port=PORT)
        for q in qs[:per_thread]:
            c.execute_command("VSEARCH", col, "FP32", q, "K", 10)
        done.append(per_thread)

    t0 = time.perf_counter()
    ts = [threading.Thread(target=worker) for _ in range(threads)]
    for t in ts:
        t.start()
    for t in ts:
        t.join()
    qps = sum(done) / (time.perf_counter() - t0)

    mem = r.info("memory")
    r.execute_command("VDROP", col)
    return {
        "n": n, "dim": dim, "insert": insert_rate,
        "p50": pct(plain, 50), "p99": pct(plain, 99),
        "f_p50": pct(filtered, 50), "qps": qps,
        "est_mib": mem["used_memory_estimated"] / 2**20,
        "heap_mib": mem["used_memory_heap_live"] / 2**20,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default="./bin/lawliet")
    ap.add_argument("--sizes", default="10000,50000")
    ap.add_argument("--dims", default="384,1536")
    ap.add_argument("--queries", type=int, default=200)
    ap.add_argument("--threads", type=int, default=8)
    args = ap.parse_args()

    proc = start_server(os.path.abspath(args.bin))
    rng = np.random.default_rng(0)
    try:
        rows = [bench_case(n, d, args.queries, args.threads, rng)
                for d in map(int, args.dims.split(",")) for n in map(int, args.sizes.split(","))]
    finally:
        proc.terminate()
        proc.wait()

    print(f"\nMachine: {platform.platform()} | {platform.processor() or platform.machine()} | "
          f"{os.cpu_count()} CPUs | Flat index, cosine, K=10, {args.queries} queries, "
          f"{args.threads} client threads for QPS\n")
    print("| Records | Dim | Insert (vec/s) | Search p50 | Search p99 | Filtered p50 (10%) | QPS | Est. memory | Live heap |")
    print("|---|---|---|---|---|---|---|---|---|")
    for x in rows:
        print(f"| {x['n']:,} | {x['dim']} | {x['insert']:,.0f} | {x['p50']:.1f} ms | {x['p99']:.1f} ms | "
              f"{x['f_p50']:.1f} ms | {x['qps']:,.0f} | {x['est_mib']:.0f} MiB | {x['heap_mib']:.0f} MiB |")


if __name__ == "__main__":
    main()
