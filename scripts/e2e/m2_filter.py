"""M2 end-to-end check: filtered top-K must match NumPy on the filtered subset,
and TEXT / META must round-trip.

Usage:
    pip install redis numpy
    ./bin/lawliet &
    python scripts/e2e/m2_filter.py [--port 6380] [--n 10000] [--dim 384]
"""

import argparse
import json
import sys
import time

import numpy as np
import redis

TOL = 1e-4

# (filter expression, numpy mask function over the metadata columns)
FILTERS = [
    ('cat == 3', lambda c: c["cat"] == 3),
    ('cat in [1, 2, 7]', lambda c: np.isin(c["cat"], [1, 2, 7])),
    ('lang == "vi" and page >= 50', lambda c: (c["lang"] == "vi") & (c["page"] >= 50)),
    ('tags contains "faq" or cat == 0', lambda c: c["faq"] | (c["cat"] == 0)),
    ('not (lang == "en" or price < 0.5)', lambda c: ~((c["lang"] == "en") | (c["price"] < 0.5))),
    ('page > 98 and cat == 9', lambda c: (c["page"] > 98) & (c["cat"] == 9)),  # tiny subset (< K)
    ('cat == 42', lambda c: np.zeros(len(c["cat"]), dtype=bool)),  # no match
]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=6380)
    ap.add_argument("--n", type=int, default=10000)
    ap.add_argument("--dim", type=int, default=384)
    ap.add_argument("--k", type=int, default=10)
    ap.add_argument("--queries", type=int, default=10)
    args = ap.parse_args()
    n, dim, k = args.n, args.dim, args.k

    r = redis.Redis(port=args.port)
    rng = np.random.default_rng(7)
    col = "e2e_filter"
    r.execute_command("VDROP", col)
    r.execute_command("VCREATE", col, "DIM", dim, "METRIC", "cosine")

    data = rng.standard_normal((n, dim)).astype("<f4")
    cols = {
        "cat": np.arange(n) % 10,
        "page": rng.integers(0, 100, n),
        "lang": np.where(rng.random(n) < 0.5, "vi", "en"),
        "price": rng.random(n),
        "faq": rng.random(n) < 0.2,
    }

    pipe = r.pipeline(transaction=False)
    for i in range(n):
        meta = {
            "cat": int(cols["cat"][i]),
            "page": int(cols["page"][i]),
            "lang": str(cols["lang"][i]),
            "price": float(cols["price"][i]),
            "tags": ["faq", "kb"] if cols["faq"][i] else ["kb"],
        }
        pipe.execute_command("VADD", col, f"doc:{i}", "FP32", data[i].tobytes(),
                             "TEXT", f"chunk số {i}", "META", json.dumps(meta))
        if (i + 1) % 1000 == 0:
            pipe.execute()
    pipe.execute()

    # TEXT / META round trip
    text, meta_json = r.execute_command("VGET", col, "doc:5")
    meta = json.loads(meta_json)
    assert text.decode() == "chunk số 5", text
    assert meta["cat"] == 5 and meta["tags"] in (["kb"], ["faq", "kb"]), meta
    print("TEXT/META round trip OK")

    normed = data.astype(np.float64)
    normed /= np.linalg.norm(normed, axis=1, keepdims=True)

    all_ok = True
    for expr, mask_fn in FILTERS:
        mask = mask_fn(cols)
        subset = np.flatnonzero(mask)
        bad, ms = 0, []
        for _ in range(args.queries):
            q = rng.standard_normal(dim).astype("<f4")
            t0 = time.perf_counter()
            hits = r.execute_command("VSEARCH", col, "FP32", q.tobytes(), "K", k,
                                     "FILTER", expr, "WITHTEXT", "WITHMETA")
            ms.append((time.perf_counter() - t0) * 1000)

            q64 = q.astype(np.float64)
            scores = normed @ (q64 / np.linalg.norm(q64))
            want = np.sort(scores[subset])[::-1][:k]

            rows = [int(h[0].decode().split(":")[1]) for h in hits]
            ok = (
                len(rows) == len(want)
                and all(mask[i] for i in rows)
                and np.allclose(scores[rows], want, rtol=TOL, atol=TOL)
                and all(h[2].decode() == f"chunk số {i}" for h, i in zip(hits, rows))
                and all(json.loads(h[3])["cat"] == cols["cat"][i] for h, i in zip(hits, rows))
            )
            bad += not ok
        all_ok &= bad == 0
        print(f"{expr:40} matches {len(subset):5} | p50 {np.median(ms):5.1f} ms | "
              f"wrong {bad}/{args.queries} | {'OK' if bad == 0 else 'FAIL'}")

    r.execute_command("VDROP", col)
    sys.exit(0 if all_ok else 1)


if __name__ == "__main__":
    main()
