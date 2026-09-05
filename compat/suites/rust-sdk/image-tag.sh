#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTEXT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_HASH=$(find "$SCRIPT_DIR" -type f \( \
  -name '*.rs' -o -name 'Cargo.toml' -o -name 'Cargo.lock' \
  -o -name 'Dockerfile' -o -name 'run.sh' -o -name 'image-tag.sh' \) \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)
# Both halves of the registry: the image carries registry.generated.json too, so
# a regenerated file has to produce a new tag or the suite would keep running
# the groups baked into the previous image. Only the digests are folded in —
# md5sum also prints the path it was given, and $CONTEXT_DIR differs per machine.
REGISTRY_HASH=$(md5sum "$CONTEXT_DIR/registry.json" "$CONTEXT_DIR/registry.generated.json" \
  | awk '{print $1}' | md5sum | cut -c1-12)

printf '%s-%s\n' "$SRC_HASH" "$REGISTRY_HASH"
