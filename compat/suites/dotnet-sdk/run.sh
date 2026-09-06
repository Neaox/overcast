#!/bin/sh
set -e

IMAGE="oc-dotnet-sdk-compat"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# The build context is compat/, not compat/suites/: the suite's unit tests run
# during the image build (see the Dockerfile) and one of them is the shared
# error-matching conformance set under compat/model/testdata/errors, which every
# backend has to answer identically. A context stopping at compat/suites/ could
# not reach it, and a fixture a suite silently does not run looks exactly like
# one it passes. Dockerfile.dockerignore keeps the wider context cheap.
CONTEXT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# bin/ and obj/ are pruned: they hold generated sources (AssemblyInfo.cs) whose
# content varies by machine and configuration, so hashing them would rebuild the
# image for reasons the image does not depend on. 'Dockerfile*' rather than
# 'Dockerfile': Dockerfile.dockerignore decides what reaches the daemon, so an
# edit to it changes the build and must change the tag.
SRC_HASH=$(find "$SCRIPT_DIR" \( -name bin -o -name obj \) -prune -o \
  -type f \( -name '*.cs' -o -name '*.csproj' -o -name 'Dockerfile*' -o -name 'run.sh' \) -print \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)

# The fixtures are found rather than globbed, so a rename or a subdirectory
# still lands in the hash — and an empty set stops the run instead of hashing
# nothing, which would silently pin one tag across every future fixture change.
FIXTURES=$(find "$CONTEXT_DIR/model/testdata/errors" -type f | sort)
if [ -z "$FIXTURES" ]; then
  echo "[dotnet-sdk] no error fixtures under $CONTEXT_DIR/model/testdata/errors" >&2
  exit 1
fi
REGISTRY_HASH=$( { cat "$CONTEXT_DIR/suites/registry.json" "$CONTEXT_DIR/suites/registry.generated.json"; \
  echo "$FIXTURES" | xargs cat; } | md5sum | cut -c1-12)
VERSIONED_IMAGE="${IMAGE}:${SRC_HASH}-${REGISTRY_HASH}"

# Retry docker build up to 3 times to handle transient TLS / registry timeouts.
# Plain progress, captured to a file: stdout must stay clean (the runner's
# NDJSON channel), and quiet mode swallows the failing RUN's real output. On
# failure the tail of the log is replayed to stderr for the runner to forward.
docker_build_with_retry() {
  _attempts=3
  _delay=10
  _i=1
  _log="${TMPDIR:-/tmp}/oc-dotnet-sdk-build-$$.log"
  while [ $_i -le $_attempts ]; do
    if DOCKER_BUILDKIT=1 docker build --progress=plain -f "$SCRIPT_DIR/Dockerfile" -t "$VERSIONED_IMAGE" "$CONTEXT_DIR" >"$_log" 2>&1; then
      rm -f "$_log"
      return 0
    fi
    echo "[dotnet-sdk] build failed (attempt $_i/$_attempts) — last 100 lines of build output:" >&2
    tail -n 100 "$_log" >&2
    if [ $_i -eq $_attempts ]; then
      echo "[dotnet-sdk] build failed after $_attempts attempts" >&2
      rm -f "$_log"
      return 1
    fi
    echo "[dotnet-sdk] retrying in ${_delay}s…" >&2
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
