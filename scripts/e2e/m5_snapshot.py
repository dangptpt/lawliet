"""M5 end-to-end check: snapshots survive restarts.

Starts its own servers (port 6392) in a temporary --dir.

Usage:
    pip install redis numpy
    go build -o bin/lawliet ./cmd/lawliet
    python scripts/e2e/m5_snapshot.py [--bin ./bin/lawliet]
"""

import argparse
import json
import os
import signal
import subprocess
import sys
import tempfile
import time

import numpy as np
import redis

PORT = 6392
N, DIM = 10000, 384


def check(cond, msg):
    print(("OK   " if cond else "FAIL ") + msg)
    return cond


class Server:
    def __init__(self, binary, workdir, *extra):
        self.log = open(os.path.join(workdir, "server.log"), "a")
        self.proc = subprocess.Popen([binary, "--port", str(PORT), "--dir", workdir, *extra],
                                     stderr=self.log)
        self.r = redis.Redis(port=PORT)
        for _ in range(200):
            if self.proc.poll() is not None:
                return
            try:
                self.r.ping()
                return
            except redis.ConnectionError:
                time.sleep(0.05)

    def stop(self, sig=signal.SIGTERM):
        self.proc.send_signal(sig)
        code = self.proc.wait(timeout=30)
        self.log.close()
        return code


def vinfo(r, col):
    v = r.execute_command("VINFO", col)
    return v if isinstance(v, dict) else dict(zip(v[::2], v[1::2]))


def snapshot_queries(r, queries):
    out = []
    for q, flt in queries:
        args = ["VSEARCH", "docs", "FP32", q, "K", 10, "WITHTEXT", "WITHMETA"]
        if flt:
            args += ["FILTER", flt]
        out.append([(h[0], h[1], h[2], h[3]) for h in r.execute_command(*args)])
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--bin", default="./bin/lawliet")
    args = ap.parse_args()
    binary = os.path.abspath(args.bin)
    workdir = tempfile.mkdtemp(prefix="lawliet-e2e-")
    path = os.path.join(workdir, "dump.lwdb")
    rng = np.random.default_rng(5)
    ok = True

    # --- 1. Load data, auto-save rule "1 1" -------------------------------------
    s = Server(binary, workdir, "--save", "1 1")
    r = s.r
    r.execute_command("VCREATE", "docs", "DIM", DIM)
    r.execute_command("VCREATE", "small", "DIM", 3, "METRIC", "l2", "MAX_VECTORS", 50, "POLICY", "allkeys-lru")
    r.execute_command("VCREATE", "sess", "DIM", 2, "POLICY", "volatile-ttl")
    data = rng.standard_normal((N, DIM)).astype("<f4")
    pipe = r.pipeline(transaction=False)
    for i in range(N):
        meta = {"src": f"f{i % 7}.md", "page": i % 100, "tags": ["a", "b"] if i % 2 else ["a"]}
        pipe.execute_command("VADD", "docs", f"doc:{i}", "FP32", data[i].tobytes(),
                             "TEXT", f"đoạn {i}", "META", json.dumps(meta))
        if (i + 1) % 1000 == 0:
            pipe.execute()
    pipe.execute()
    for i in range(20):
        r.execute_command("VADD", "small", f"s{i}", "VALUES", 3, i, 0, 0)
    r.execute_command("VADD", "sess", "short", "VALUES", 2, 1, 0, "PX", 1500)
    r.execute_command("VADD", "sess", "long", "VALUES", 2, 1, 0, "EX", 3600)
    r.execute_command("VADD", "sess", "pinned", "VALUES", 2, 1, 0)

    deadline = time.time() + 5
    while time.time() < deadline and r.info("persistence")["rdb_changes_since_last_save"] != 0:
        time.sleep(0.2)
    pinfo = r.info("persistence")
    ok &= check(pinfo["rdb_changes_since_last_save"] == 0 and os.path.exists(path),
                f"auto-save rule wrote {os.path.getsize(path) / 2**20:.1f} MiB in "
                f"{pinfo['rdb_last_save_duration_ms']} ms")

    queries = [(rng.standard_normal(DIM).astype("<f4").tobytes(), f) for f in
               (None, 'src == "f3.md" and page < 50', 'tags contains "b"')]
    before = snapshot_queries(r, queries)
    before_info = {c: vinfo(r, c) for c in ("docs", "small", "sess")}

    # --- 2. Graceful shutdown saves the latest writes -----------------------------
    r.execute_command("VADD", "docs", "late", "FP32", data[0].tobytes(), "TEXT", "written just before stop")
    code = s.stop(signal.SIGTERM)
    ok &= check(code == 0, f"SIGTERM: clean exit (code {code})")
    time.sleep(1.6)  # "short" (PX 1500) expires while the server is down

    s = Server(binary, workdir, "--save", "")
    r = s.r
    ok &= check(r.execute_command("VCOUNT", "docs") == N + 1, "all docs restored, incl. write just before SIGTERM")
    ok &= check(snapshot_queries(r, queries) == before, "search results (scores, text, meta, filters) identical after restart")
    after_info = {c: vinfo(r, c) for c in ("docs", "small", "sess")}
    same_cfg = all(after_info[c][k] == before_info[c][k]
                   for c in after_info for k in (b"dim", b"metric", b"max_vectors", b"policy"))
    ok &= check(same_cfg, "collection config (dim, metric, max_vectors, policy) restored")
    ok &= check(r.execute_command("VEXISTS", "sess", "short") == 0, "record that expired while down is gone")
    ttl = r.execute_command("VTTL", "sess", "long")
    ok &= check(3590 <= ttl <= 3600, f"TTL kept as absolute time ({ttl}s left)")
    ok &= check(r.execute_command("VTTL", "sess", "pinned") == -1, "record without TTL has none")

    # --- 3. Crash (kill -9): only what was saved survives -------------------------
    r.execute_command("VADD", "docs", "saved", "FP32", data[1].tobytes())
    r.execute_command("SAVE")
    r.execute_command("VADD", "docs", "unsaved", "FP32", data[2].tobytes())
    s.stop(signal.SIGKILL)
    s = Server(binary, workdir, "--save", "")
    r = s.r
    ok &= check(r.execute_command("VEXISTS", "docs", "saved") == 1 and
                r.execute_command("VEXISTS", "docs", "unsaved") == 0,
                "after kill -9: data up to the last SAVE survives, later writes are lost")
    s.stop()

    # --- 4. A corrupt file stops the server instead of loading garbage -----------
    with open(path, "r+b") as f:
        f.seek(os.path.getsize(path) // 2)
        b = f.read(1)
        f.seek(-1, 1)
        f.write(bytes([b[0] ^ 0xFF]))
    s = Server(binary, workdir)
    code = s.proc.wait(timeout=30)
    log = open(os.path.join(workdir, "server.log")).read()
    ok &= check(code == 1 and "checksum mismatch" in log,
                f"corrupt snapshot: refuses to start (exit {code}, logs checksum mismatch)")

    print(f"     workdir: {workdir}")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
