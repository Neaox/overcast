#!/usr/bin/env bash
# compat/dev.sh — Interactive compat dashboard with hot-reloading UI.
#
# 1. Install UI dependencies if needed.
# 2. Build the compat CLI binary (bin/compat).
# 3. Start Overcast if not running.
# 4. Launch compat server in interactive mode (--serve --interactive --no-ui).
# 5. Start Vite dev server for UI with HMR (proxies API to compat server).
# 6. Open browser at Vite dev server URL.
#
# No tests run until triggered from the UI or via MCP.
# UI changes hot-reload instantly — no rebuild needed.
#
# Usage:
#   ./compat/dev.sh                    # all defaults
#   ./compat/dev.sh -p 4567            # explicit overcast port
#   OVERCAST_PORT=4566 ./compat/dev.sh # explicit port (env var)
#   COMPAT_PORT=8888   ./compat/dev.sh # use a different dashboard port
#
# Options:
#   -p, --port PORT   Overcast port (default: 4566)
#   -h, --help        Show this help
#
# Prerequisites: Node.js 20+, Go 1.24+, Docker (optional, for Overcast container)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Parse CLI arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    -p|--port)
      OVERCAST_PORT="$2"
      shift 2
      ;;
    -h|--help)
      awk '/^# Usage:/{f=1} f && /^[^#]/{exit} f' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      echo "Usage: $0 [-p PORT] [-h]" >&2
      exit 1
      ;;
  esac
done

OVERCAST_PORT="${OVERCAST_PORT:-4566}"
COMPAT_PORT="${COMPAT_PORT:-7777}"
VITE_PORT="${VITE_PORT:-5173}"
OVERCAST_ENDPOINT="http://localhost:${OVERCAST_PORT}"
COMPAT_ADDR=":${COMPAT_PORT}"
COMPAT_URL="http://localhost:${COMPAT_PORT}"
VITE_URL="http://localhost:${VITE_PORT}"

cd "$REPO_ROOT"

# ── Cleanup ───────────────────────────────────────────────────────────────────
PIDS_TO_KILL=()
cleanup() {
  for pid in "${PIDS_TO_KILL[@]}"; do
    kill "$pid" 2>/dev/null
  done
  # Wait up to 5s for processes to exit, then force-kill stragglers.
  sleep 5
  for pid in "${PIDS_TO_KILL[@]}"; do
    kill -9 "$pid" 2>/dev/null
  done
}
trap cleanup INT TERM EXIT

# ── Step 1: install UI dependencies ──────────────────────────────────────────
echo "▶ Checking UI dependencies…"
if [[ ! -d compat/ui/node_modules ]]; then
  echo "  Installing npm packages…"
  (cd compat/ui && npm install --silent)
fi
echo "  ✓ UI dependencies ready"

# ── Step 2: build the compat binary ──────────────────────────────────────────
echo "▶ Building bin/compat…"
go build -o bin/compat ./cmd/compat
echo "  ✓ bin/compat ready"

# ── Step 3: ensure Overcast is running ────────────────────────────────────────
if curl -sf "${OVERCAST_ENDPOINT}/_health" >/dev/null 2>&1; then
  echo "▶ Overcast already running at ${OVERCAST_ENDPOINT}"
else
  echo "▶ Starting Overcast on port ${OVERCAST_PORT}…"
  # Prefer a native run; fall back to Docker if the binary isn't built yet.
  if [[ -x bin/overcast ]]; then
    OVERCAST_PORT="${OVERCAST_PORT}" bin/overcast serve &
    PIDS_TO_KILL+=($!)
  else
    docker run --rm -d \
      -p "${OVERCAST_PORT}:4566" \
      --name overcast-compat \
      ghcr.io/your-org/overcast:latest >/dev/null || \
    docker compose up -d 2>/dev/null || {
      echo "  ✗ Could not start Overcast. Build it first: make build" >&2
      exit 1
    }
  fi

  # Wait up to 10 s for Overcast to become healthy.
  for i in $(seq 1 20); do
    if curl -sf "${OVERCAST_ENDPOINT}/_health" >/dev/null 2>&1; then
      echo "  ✓ Overcast ready"
      break
    fi
    sleep 0.5
    if [[ $i -eq 20 ]]; then
      echo "  ✗ Overcast did not become ready in time" >&2
      exit 1
    fi
  done
fi

# ── Step 4: launch the compat server (interactive, no embedded UI) ───────────
echo "▶ Starting compat server on ${COMPAT_URL} (interactive mode)…"
bin/compat \
  --endpoint "${OVERCAST_ENDPOINT}" \
  --serve \
  --interactive \
  --no-ui \
  --port "${COMPAT_ADDR}" &
PIDS_TO_KILL+=($!)

# Wait for compat API to be ready.
for i in $(seq 1 40); do
  if curl -sf "${COMPAT_URL}/suites" >/dev/null 2>&1; then
    echo "  ✓ Compat server ready"
    break
  fi
  sleep 0.25
  if [[ $i -eq 40 ]]; then
    echo "  ✗ Compat server did not start in time" >&2
    exit 1
  fi
done

# ── Step 5: start Vite dev server (HMR) ─────────────────────────────────────
echo "▶ Starting Vite dev server…"
(cd compat/ui && npx vite --port "${VITE_PORT}") &
PIDS_TO_KILL+=($!)

# Wait for Vite to be ready.
for i in $(seq 1 40); do
  if curl -sf "${VITE_URL}" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
echo "  ✓ Vite dev server ready"

# ── Step 6: open the browser ────────────────────────────────────────────────
echo "▶ Opening ${VITE_URL}"
if [[ -n "${BROWSER:-}" ]]; then
  "${BROWSER}" "${VITE_URL}" &>/dev/null &
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "${VITE_URL}" &>/dev/null &
else
  python3 -m webbrowser "${VITE_URL}" &>/dev/null &
fi

echo ""
echo "  UI (HMR):     ${VITE_URL}"
echo "  Compat API:   ${COMPAT_URL}"
echo "  Overcast:     ${OVERCAST_ENDPOINT}"
echo ""
echo "  UI changes hot-reload instantly — no rebuild needed."
echo "  Tests run on demand from the UI or via MCP."
echo "  Press Ctrl+C to stop."
echo ""

# Keep running until Ctrl+C or a child exits.
wait ${PIDS_TO_KILL[@]} 2>/dev/null || true
