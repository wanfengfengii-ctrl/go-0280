#!/usr/bin/env bash
# Deterministic local smoke test: builds and starts the service, probes its
# health endpoint, exercises a real catalog -> task -> lock API flow, then
# cleans up every process and temporary file. No external network is used.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

PORT="${SMOKE_PORT:-18087}"
TMP_DIR="$(mktemp -d)"
DB="$TMP_DIR/silage-smoke.db"
BIN="$TMP_DIR/server"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "[smoke] building server"
go build -o "$BIN" ./cmd/server

echo "[smoke] starting server on :$PORT"
DB_PATH="$DB" ADDR=":$PORT" "$BIN" &
SERVER_PID=$!

# Wait for the health endpoint to become ready.
ready=0
for _ in $(seq 1 100); do
  if resp="$(curl -sS "http://127.0.0.1:$PORT/api/health" 2>/dev/null)"; then
    if printf '%s' "$resp" | grep -q '"status":"ok"'; then
      ready=1
      break
    fi
  fi
  sleep 0.1
done
if [[ "$ready" != "1" ]]; then
  echo "[smoke] server did not become ready" >&2
  exit 1
fi

health="$(curl -sS "http://127.0.0.1:$PORT/api/health")"
if ! printf '%s' "$health" | grep -q '"status":"ok"'; then
  echo "[smoke] health check failed: $health" >&2
  exit 1
fi
echo "[smoke] health ok: $health"

# Create a catalog.
curl -sS -X POST -H 'Content-Type: application/json' \
  -d '{"id":"c-smoke","version":1,"plot_id":"p1","harvest_batch_id":"b1","zones":["A"],"layers":{"A":[1,2]},"depths":[0,1],"adjacency":{"A:1":["A:2"],"A:2":["A:1"]},"open_face":"f1","ventilator":"v1","oxygen_min":180,"h2s_max":5,"scale":1}' \
  "http://127.0.0.1:$PORT/api/catalogs" > "$TMP_DIR/catalog.json"

# Create a task and capture its id.
task_resp="$(curl -sS -X POST -H 'Content-Type: application/json' \
  -d '{"silo_id":"s-smoke"}' "http://127.0.0.1:$PORT/api/tasks")"
task_id="$(printf '%s' "$task_resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
if [[ -z "$task_id" ]]; then
  echo "[smoke] failed to extract task id from: $task_resp" >&2
  exit 1
fi
echo "[smoke] created task $task_id"

# Lock the task against the catalog snapshot.
lock_resp="$(curl -sS -X POST -H 'Content-Type: application/json' \
  -d "{\"operation_id\":\"op-lock\",\"expected_generation\":0,\"snapshot_id\":\"c-smoke\"}" \
  "http://127.0.0.1:$PORT/api/tasks/$task_id/lock")"
if ! printf '%s' "$lock_resp" | grep -q '"status":"film_check"'; then
  echo "[smoke] lock failed: $lock_resp" >&2
  exit 1
fi
echo "[smoke] lock ok"

# Verify the snapshot exposes the sampling grid.
snap="$(curl -sS "http://127.0.0.1:$PORT/api/tasks/$task_id")"
if ! printf '%s' "$snap" | grep -q '"cells"'; then
  echo "[smoke] snapshot missing sampling grid: $snap" >&2
  exit 1
fi
echo "[smoke] snapshot exposes sampling grid"

echo "[smoke] PASS"
