"""M1 end-to-end check: Lawliet top-K must match NumPy brute force.

Usage:
    pip install redis numpy
    ./bin/lawliet &            # default port 6380
    python scripts/e2e/m1_topk.py [--port 6380] [--n 10000] [--dim 384]
"""

import argparse
import sys
import time

import numpy as np
import redis

TOL = 1e-4


def true_scores(metric, data, q):
    """Score of every row in float64, same convention as Lawliet (higher is better)."""
    if metric == "cosine":
        return (data / np.linalg.norm(data, axis=1, keepdims=True)) @ (q / np.linalg.norm(q))
    if metric == "ip":
        return data @ q
    return -np.linalg.norm(data - q, axis=1)  # l2: negated distance


def check_metric(r, metric, n, dim, k, queries, rng):
    col = f"e2e_{metric}"
    r.execute_command("VDROP", col)
    r.execute_command("VCREATE", col, "DIM", dim, "METRIC", metric)

    data = rng.standard_normal((n, dim)).astype("<f4")  # float32, little-endian
    t0 = time.perf_counter()
    pipe = r.pipeline(transaction=False)
    for i, v in enumerate(data):
        pipe.execute_command("VADD", col, f"doc:{i}", "FP32", v.tobytes())
        if (i + 1) % 1000 == 0:
            pipe.execute()
    pipe.execute()
    insert_s = time.perf_counter() - t0

    count = r.execute_command("VCOUNT", col)
    assert count == n, f"VCOUNT={count}, want {n}"

    data64 = data.astype(np.float64)
    bad_queries, max_err, search_ms = 0, 0.0, []
    for _ in range(queries):
        q = rng.standard_normal(dim).astype("<f4")
        t0 = time.perf_counter()
        hits = r.execute_command("VSEARCH", col, "FP32", q.tobytes(), "K", k)
        search_ms.append((time.perf_counter() - t0) * 1000)

        scores = true_scores(metric, data64, q.astype(np.float64))
        want = np.sort(scores)[::-1][:k]  # best k true scores

        got_rows = [int(h[0].decode().split(":")[1]) for h in hits]
        got_scores = np.array([float(h[1]) for h in hits])

        # Rank j is correct if the returned row's true score equals the j-th best
        # true score: accepts swaps between tied rows, rejects wrong results.
        ranks_ok = len(got_rows) == k and np.allclose(scores[got_rows], want, rtol=TOL, atol=TOL)
        if not ranks_ok:
            bad_queries += 1
        max_err = max(max_err, float(np.max(np.abs(got_scores - want) / np.maximum(1, np.abs(want)))))

    r.execute_command("VDROP", col)
    ok = bad_queries == 0 and max_err < TOL
    print(
        f"{metric:6} n={n} dim={dim} k={k} | insert {n / insert_s:,.0f} vec/s | "
        f"search p50 {np.median(search_ms):.1f} ms | wrong queries {bad_queries}/{queries} | "
        f"max rel score err {max_err:.1e} | {'OK' if ok else 'FAIL'}"
    )
    return ok


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=6380)
    ap.add_argument("--n", type=int, default=10000)
    ap.add_argument("--dim", type=int, default=384)
    ap.add_argument("--k", type=int, default=10)
    ap.add_argument("--queries", type=int, default=20)
    args = ap.parse_args()

    r = redis.Redis(port=args.port)  # default client settings (RESP3 on redis-py 8.x)
    rng = np.random.default_rng(42)
    results = [check_metric(r, m, args.n, args.dim, args.k, args.queries, rng) for m in ("cosine", "ip", "l2")]
    sys.exit(0 if all(results) else 1)


if __name__ == "__main__":
    main()
