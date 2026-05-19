#!/bin/sh
set -e

IMAGE="oc-dotnet-sdk-compat"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTEXT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_HASH=$(find "$SCRIPT_DIR" -type f \( -name '*.cs' -o -name '*.csproj' -o -name 'Dockerfile' -o -name 'run.sh' \) \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)
REGISTRY_HASH=$(md5sum "$CONTEXT_DIR/registry.json" | cut -c1-12)
VERSIONED_IMAGE="${IMAGE}:${SRC_HASH}-${REGISTRY_HASH}"

# Retry docker build up to 3 times to handle transient TLS / registry timeouts.
docker_build_with_retry() {
  _attempts=3
  _delay=10
  _i=1
  while [ $_i -le $_attempts ]; do
    if DOCKER_BUILDKIT=1 docker build -q -f "$SCRIPT_DIR/Dockerfile" -t "$VERSIONED_IMAGE" "$CONTEXT_DIR"; then
      return 0
    fi
    if [ $_i -eq $_attempts ]; then
      echo "[dotnet-sdk] build failed after $_attempts attempts" >&2
      return 1
    fi
    echo "[dotnet-sdk] build failed (attempt $_i/$_attempts), retrying in ${_delay}s…" >&2
    sleep $_delay
    _delay=$((_delay * 2))
    _i=$((_i + 1))
  done
}

if ! docker image inspect "$VERSIONED_IMAGE" > /dev/null 2>&1; then
  echo "[dotnet-sdk] building image (hash ${SRC_HASH}-${REGISTRY_HASH})..." >&2
  docker_build_with_retry
  docker tag "$VERSIONED_IMAGE" "${IMAGE}:latest"
  # Reclaim disk: drop superseded tags of this image and any dangling layers
  # left behind by --no-cache rebuilds. Best-effort — don't fail the suite.
  docker images --format '{{.Repository}}:{{.Tag}}' "$IMAGE" 2>/dev/null \
    | grep -v -e "^${VERSIONED_IMAGE}$" -e "^${IMAGE}:latest$" \
    | xargs -r docker rmi -f >/dev/null 2>&1 || true
  docker image prune -f >/dev/null 2>&1 || true
fi

if [ -f "/.dockerenv" ]; then
  NETWORK="--network container:$(hostname)"
else
  NETWORK="--network host"
fi

# In interactive mode, keep stdin open for the command protocol.
INTERACTIVE_FLAGS=""
if [ "${OVERCAST_COMPAT_INTERACTIVE:-}" = "1" ]; then
  INTERACTIVE_FLAGS="-i"
fi

exec docker run --rm $INTERACTIVE_FLAGS \
  $NETWORK \
  -e OVERCAST_ENDPOINT \
  -e OVERCAST_DEFAULT_REGION \
  -e OVERCAST_COMPAT_RUN_ID \
  -e OVERCAST_COMPAT_SERVICE \
  -e OVERCAST_COMPAT_GROUPS \
  -e OVERCAST_COMPAT_TESTS \
  -e OVERCAST_COMPAT_TEST_PAIRS \
  -e OVERCAST_COMPAT_PARALLEL_SLOTS \
  -e OVERCAST_COMPAT_INTERACTIVE \
  "$VERSIONED_IMAGE"
