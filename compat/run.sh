#!/usr/bin/env bash
# compat/run.sh — Interactive compat dashboard with pre-built UI.
#
# 1. Build the compat UI (compat/ui/dist/).
# 2. Build the compat CLI binary (bin/compat).
# 3. Start Overcast if not running.
# 4. Launch compat server in interactive mode (--serve --interactive).
# 5. Open browser at http://localhost:7777.
#
# No tests run until triggered from the UI or via MCP.
# Use this for a stable, production-like experience.
# For development with hot-reloading UI, use dev.sh instead.
#
# Usage:
#   ./compat/run.sh                    # all defaults
#   OVERCAST_PORT=4566 ./compat/run.sh # explicit port
#   COMPAT_PORT=8888   ./compat/run.sh # use a different dashboard port
#
# Prerequisites: Node.js 20+, Go 1.24+, Docker (optional, for Overcast container)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OVERCAST_PORT="${OVERCAST_PORT:-4566}"
COMPAT_PORT="${COMPAT_PORT:-7777}"
OVERCAST_ENDPOINT="http://localhost:${OVERCAST_PORT}"
COMPAT_ADDR=":${COMPAT_PORT}"
COMPAT_URL="http://localhost:${COMPAT_PORT}"

cd "$REPO_ROOT"

# ── Cleanup ───────────────────────────────────────────────────────────────────
PIDS_TO_KILL=()
cleanup() {
  for pid in "${PIDS_TO_KILL[@]}"; do
    kill "$pid" 2>/dev/null
  done
}
trap cleanup INT TERM EXIT

# ── Step 1: build the compat UI ──────────────────────────────────────────────
echo "▶ Building compat UI…"
(
  cd compat/ui
  if [[ ! -d node_modules ]]; then
    npm install --silent
  fi
  npm run build --silent
)
echo "  ✓ UI built → compat/ui/dist/"

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
    OVERCAST_PORT="${OVERCAST_PORT}" bin/overcast &
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

# ── Step 4: launch the compat server (interactive, embedded UI) ──────────────
echo "▶ Starting compat dashboard on ${COMPAT_URL} (interactive mode)…"
bin/compat \
  --endpoint "${OVERCAST_ENDPOINT}" \
  --serve \
  --interactive \
  --port "${COMPAT_ADDR}" &
PIDS_TO_KILL+=($!)

# Wait for the dashboard to start accepting connections.
for i in $(seq 1 40); do
  if curl -sf "${COMPAT_URL}/suites" >/dev/null 2>&1; then
    echo "  ✓ Compat server ready"
    break
  fi
  sleep 0.25
  if [[ $i -eq 40 ]]; then
    echo "  ✗ Dashboard did not start in time" >&2
    exit 1
  fi
done

# ── Step 5: open the browser ────────────────────────────────────────────────
echo "▶ Opening ${COMPAT_URL}"
if [[ -n "${BROWSER:-}" ]]; then
  "${BROWSER}" "${COMPAT_URL}" &>/dev/null &
elif command -v xdg-open >/dev/null 2>&1; then
  xdg-open "${COMPAT_URL}" &>/dev/null &
else
  python3 -m webbrowser "${COMPAT_URL}" &>/dev/null &
fi

echo ""
echo "  Dashboard:    ${COMPAT_URL}"
echo "  Overcast:     ${OVERCAST_ENDPOINT}"
echo ""
echo "  Tests run on demand from the UI or via MCP."
echo "  For hot-reloading UI development, use dev.sh instead."
echo "  Press Ctrl+C to stop."
echo ""

# Keep running until Ctrl+C or the compat process exits.
wait
