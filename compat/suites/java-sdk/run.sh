#!/bin/sh
# run.sh — build (if needed) and execute the java-sdk compat suite via Docker.
# Called by the compat runner instead of `java -jar` directly, since Maven and
# the JDK are not assumed to be present on the host.
set -e

IMAGE="oc-java-sdk-compat"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# Build context is compat/suites/ so that registry.json is accessible.
CONTEXT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Compute a content hash of the Java sources, pom.xml and both registry files
# so that the image is automatically rebuilt whenever any of those change.
# (Docker layer caching is unreliable on Windows host volumes via 9p.)
SRC_HASH=$(find "$SCRIPT_DIR/src" "$SCRIPT_DIR/pom.xml" \
  "$CONTEXT_DIR/registry.json" "$CONTEXT_DIR/registry.generated.json" -type f \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)
VERSIONED_IMAGE="${IMAGE}:${SRC_HASH}"

# Retry docker build up to 3 times to handle transient TLS / registry timeouts.
# The build runs with plain progress captured to a file — stdout must stay
# clean (it is the runner's NDJSON channel), and buildkit's quiet mode used to
# swallow the failing RUN's real output (run 32796853142: fifteen mvn failures,
# and the job log only ever showed the generic "exit code: 1" step summary).
# On failure the tail of the captured log is replayed to stderr, which the
# runner forwards to its own log.
docker_build_with_retry() {
  _attempts=3
  _delay=10
  _i=1
  _log="${TMPDIR:-/tmp}/oc-java-sdk-build-$$.log"
  while [ $_i -le $_attempts ]; do
    if DOCKER_BUILDKIT=1 docker build --progress=plain -f "$SCRIPT_DIR/Dockerfile" -t "$VERSIONED_IMAGE" "$CONTEXT_DIR" >"$_log" 2>&1; then
      rm -f "$_log"
      return 0
    fi
    echo "[java-sdk] build failed (attempt $_i/$_attempts) — last 100 lines of build output:" >&2
    tail -n 100 "$_log" >&2
    if [ $_i -eq $_attempts ]; then
      echo "[java-sdk] build failed after $_attempts attempts" >&2
      rm -f "$_log"
      return 1
    fi
    echo "[java-sdk] retrying in ${_delay}s…" >&2
    sleep $_delay
    _delay=$((_delay * 2))
    _i=$((_i + 1))
  done
}

if ! docker image inspect "$VERSIONED_IMAGE" > /dev/null 2>&1; then
  echo "[java-sdk] building image (hash $SRC_HASH)…" >&2
  docker_build_with_retry
  # Also tag as :latest for human convenience.
  docker tag "$VERSIONED_IMAGE" "${IMAGE}:latest"
  # Reclaim disk: drop superseded tags of this image and any dangling layers
  # left behind by --no-cache rebuilds. Best-effort — don't fail the suite.
  docker images --format '{{.Repository}}:{{.Tag}}' "$IMAGE" 2>/dev/null \
    | grep -v -e "^${VERSIONED_IMAGE}$" -e "^${IMAGE}:latest$" \
    | xargs -r docker rmi -f >/dev/null 2>&1 || true
  docker image prune -f >/dev/null 2>&1 || true
fi

# When running inside a Docker container (e.g. a VS Code dev container), the
# host Docker daemon spawns the java-sdk container as a sibling, so
# --network host resolves to the Windows/Mac host — not the dev container where
# Overcast is listening. Sharing the dev container's network namespace fixes
# this. When running directly on Linux (no outer container) --network host
# is the correct choice.
if [ -f "/.dockerenv" ]; then
  # Inside a container — share its network namespace so localhost:4566 resolves.
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
