#!/bin/sh
set -e

# Compat test runner for Rust SDK suite.
#
# Image resolution order:
#   1. Pre-pulled / locally-built ${IMAGE}:${TAG} (src+registry hash)
#   2. docker pull ${REMOTE_IMAGE}:${TAG} from GHCR (fast path for CI/fresh checkouts)
#   3. docker build locally (slow fallback)
#
# Override with OVERCAST_RUST_SKIP_PULL=1 to skip the GHCR pull.

IMAGE="oc-rust-sdk-compat"
REMOTE_IMAGE="${OVERCAST_RUST_REMOTE_IMAGE:-ghcr.io/neaox/overcast/rust-sdk-compat}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTEXT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

IN_CONTAINER=0
if [ -f "/.dockerenv" ]; then
  IN_CONTAINER=1
fi

SRC_HASH=$(find "$SCRIPT_DIR" -type f \( -name '*.rs' -o -name 'Cargo.toml' -o -name 'Cargo.lock' -o -name 'Dockerfile' -o -name 'run.sh' \) \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)
REGISTRY_HASH=$(md5sum "$CONTEXT_DIR/registry.json" | cut -c1-12)
TAG="${SRC_HASH}-${REGISTRY_HASH}"
VERSIONED_IMAGE="${IMAGE}:${TAG}"

if ! docker image inspect "$VERSIONED_IMAGE" > /dev/null 2>&1; then
  if [ "$OVERCAST_RUST_SKIP_PULL" != "1" ] && docker pull -q "${REMOTE_IMAGE}:${TAG}" > /dev/null 2>&1; then
    docker tag "${REMOTE_IMAGE}:${TAG}" "$VERSIONED_IMAGE"
  else
    echo "[rust-sdk] building image (hash ${TAG})..." >&2
    _rust_attempts=3
    _rust_delay=10
    _rust_i=1
    while [ $_rust_i -le $_rust_attempts ]; do
      if DOCKER_BUILDKIT=1 docker build -q -f "$SCRIPT_DIR/Dockerfile" -t "$VERSIONED_IMAGE" "$CONTEXT_DIR"; then
        break
      fi
      if [ $_rust_i -eq $_rust_attempts ]; then
        echo "[rust-sdk] build failed after $_rust_attempts attempts" >&2
        exit 1
      fi
      echo "[rust-sdk] build failed (attempt $_rust_i/$_rust_attempts), retrying in ${_rust_delay}s…" >&2
      sleep $_rust_delay
      _rust_delay=$((_rust_delay * 2))
      _rust_i=$((_rust_i + 1))
    done
  fi
  docker tag "$VERSIONED_IMAGE" "${IMAGE}:latest"
  # Reclaim disk: drop superseded tags of this image and any dangling layers
  # left behind by rebuilds or GHCR re-pulls. Best-effort — don't fail the suite.
  docker images --format '{{.Repository}}:{{.Tag}}' "$IMAGE" 2>/dev/null \
    | grep -v -e "^${VERSIONED_IMAGE}$" -e "^${IMAGE}:latest$" \
    | xargs -r docker rmi -f >/dev/null 2>&1 || true
  docker image prune -f >/dev/null 2>&1 || true
fi

# Network resolution: Docker Desktop (Mac/Windows/WSL2) does not actually bridge
# --network host to the host kernel, so localhost/127.0.0.1 inside the container
# can't reach overcast running on the host. Use --add-host=host.docker.internal:host-gateway
# and rewrite the endpoint.
if [ $IN_CONTAINER -eq 1 ]; then
  NETWORK="--network container:$(hostname)"
  EXTRA_HOSTS=""
else
  NETWORK=""
  EXTRA_HOSTS="--add-host=host.docker.internal:host-gateway"
  case "$OVERCAST_ENDPOINT" in
    *localhost*) OVERCAST_ENDPOINT=$(echo "$OVERCAST_ENDPOINT" | sed 's|localhost|host.docker.internal|') ;;
    *127.0.0.1*) OVERCAST_ENDPOINT=$(echo "$OVERCAST_ENDPOINT" | sed 's|127\.0\.0\.1|host.docker.internal|') ;;
  esac
  export OVERCAST_ENDPOINT
fi

# In interactive mode, keep stdin open for the command protocol.
INTERACTIVE_FLAGS=""
if [ "${OVERCAST_COMPAT_INTERACTIVE:-}" = "1" ]; then
  INTERACTIVE_FLAGS="-i"
fi

exec docker run --rm $INTERACTIVE_FLAGS \
  $NETWORK $EXTRA_HOSTS \
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
