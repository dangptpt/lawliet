#!/usr/bin/env bash
# Builds the server and runs every end-to-end check.
# Requires: go, python3 with `redis` and `numpy` installed.
set -euo pipefail
cd "$(dirname "$0")/../.."

PYTHON="${PYTHON:-python3}"
PORT=6380
go build -o bin/lawliet ./cmd/lawliet

./bin/lawliet --port "$PORT" 2>/tmp/lawliet-e2e.log &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true' EXIT
for _ in $(seq 1 100); do
  "$PYTHON" -c "import redis; redis.Redis(port=$PORT).ping()" 2>/dev/null && break
  sleep 0.1
done

status=0
run() {
  echo "=== $*"
  if ! "$PYTHON" "$@"; then status=1; echo "!!! FAILED: $*"; fi
}
# These use the server started above.
run scripts/e2e/m1_topk.py --port "$PORT"
run scripts/e2e/m2_filter.py --port "$PORT"
run scripts/e2e/m3_ttl.py --port "$PORT"
run examples/python/rag_demo.py
# These start their own servers with specific flags.
run scripts/e2e/m4_limits.py --bin ./bin/lawliet
run scripts/e2e/m5_snapshot.py --bin ./bin/lawliet

exit "$status"
