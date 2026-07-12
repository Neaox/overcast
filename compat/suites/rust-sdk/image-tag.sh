#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONTEXT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SRC_HASH=$(find "$SCRIPT_DIR" -type f \( \
  -name '*.rs' -o -name 'Cargo.toml' -o -name 'Cargo.lock' \
  -o -name 'Dockerfile' -o -name 'run.sh' -o -name 'image-tag.sh' \) \
  | sort | xargs md5sum 2>/dev/null | md5sum | cut -c1-12)
REGISTRY_HASH=$(md5sum "$CONTEXT_DIR/registry.json" | cut -c1-12)

printf '%s-%s\n' "$SRC_HASH" "$REGISTRY_HASH"
