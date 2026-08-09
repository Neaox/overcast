#!/usr/bin/env bash
# Build and publish local dev Docker images to GHCR.
#
# Defaults:
#   ghcr.io/<github-owner>/overcast:dev
#   ghcr.io/<github-owner>/overcast-slim:dev
#
# Authentication:
#   gh auth login
# or:
#   GHCR_USERNAME=<github-user> GHCR_TOKEN=<classic-or-fine-grained-pat> bash scripts/publish-dev-images.sh
#
# Requirements: docker, git, and either gh or GHCR_USERNAME/GHCR_TOKEN.
# make and a host Go/Node toolchain are NOT required — see the build helpers below.
#
# Images are published for linux/amd64 and linux/arm64 so they can be pulled on
# both x86-64 and Apple silicon. Override with GHCR_PLATFORMS, e.g. to build only
# for the local platform when iterating:
#   GHCR_PLATFORMS=linux/amd64 bash scripts/publish-dev-images.sh

set -euo pipefail

# Run from the repo root regardless of where the script was invoked from, so
# `make` finds the Makefile and `docker build .` gets the right context.
cd -- "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

log() { printf '[publish-dev-images] %s\n' "$*"; }
die() { printf '[publish-dev-images] ERROR: %s\n' "$*" >&2; exit 1; }
success() {
  if [ -t 1 ]; then
    printf '\033[32m✓ %s\033[0m\n' "$*"
  else
    printf 'SUCCESS: %s\n' "$*"
  fi
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

gh_token_scopes() {
  gh api --include user 2>/dev/null | while IFS= read -r line; do
    line="${line%$'\r'}"
    case "${line,,}" in
      x-oauth-scopes:*)
        printf '%s\n' "${line#*: }"
        return 0
        ;;
    esac
  done
}

gh_token_has_scope() {
  local wanted scopes scope
  wanted="$1"
  scopes="$(gh_token_scopes)"

  IFS=',' read -ra scope_list <<< "$scopes"
  for scope in "${scope_list[@]}"; do
    scope="${scope//[[:space:]]/}"
    if [ "$scope" = "$wanted" ]; then
      return 0
    fi
  done

  return 1
}

github_package_url() {
  local image package owner_path owner_type
  image="$1"
  package="${image##*/}"
  owner_path="users"

  if command -v gh >/dev/null 2>&1; then
    owner_type="$(gh api "users/$GHCR_OWNER" --jq .type 2>/dev/null || true)"
    if [ "$owner_type" = "Organization" ]; then
      owner_path="orgs"
    fi
  fi

  printf 'https://github.com/%s/%s/packages/container/package/%s\n' "$owner_path" "$GHCR_OWNER" "$package"
}

github_owner_from_remote() {
  local remote
  remote="$(git remote get-url origin 2>/dev/null || true)"
  remote="${remote%.git}"

  case "$remote" in
    git@github.com:*)
      remote="${remote#git@github.com:}"
      printf '%s\n' "${remote%%/*}"
      ;;
    https://github.com/*)
      remote="${remote#https://github.com/}"
      printf '%s\n' "${remote%%/*}"
      ;;
    http://github.com/*)
      remote="${remote#http://github.com/}"
      printf '%s\n' "${remote%%/*}"
      ;;
    *)
      return 1
      ;;
  esac
}

# Images are built by driving docker directly rather than through `make`, so the
# script runs on hosts without make (Windows outside the devcontainer) and so
# there is one build path rather than two. The Makefile's docker-console and
# docker-slim targets build for the host platform only, which cannot produce the
# multi-arch manifests below.
#
# No host Go or Node toolchain is needed: the Dockerfile builds the SPA and
# compiles the binary in its own stages. scripts/docker-go.sh is deliberately NOT
# used here — it runs go subcommands in a golang container that has no Docker CLI
# or socket, so it cannot build images.

# Publish for every platform in $PLATFORMS, not just the build host's. A plain
# `docker build` bakes in the host architecture, so an amd64 machine publishes a
# tag that arm64 machines (Apple silicon) cannot pull:
#   no matching manifest for linux/arm64/v8 in the manifest list entries
# The Dockerfile cross-compiles natively via --platform=$BUILDPLATFORM with
# TARGETOS/TARGETARCH, so the extra architecture costs no emulation.
PLATFORMS="${GHCR_PLATFORMS:-linux/amd64,linux/arm64}"
BUILDX_BUILDER="${GHCR_BUILDX_BUILDER:-overcast-publish}"

# The default "docker" buildx driver cannot build multi-platform; that needs a
# docker-container builder. Create one on demand rather than failing obscurely.
ensure_buildx_builder() {
  if docker buildx inspect "$BUILDX_BUILDER" >/dev/null 2>&1; then
    return 0
  fi
  log "Creating buildx builder $BUILDX_BUILDER (driver: docker-container)"
  docker buildx create --name "$BUILDX_BUILDER" --driver docker-container >/dev/null
}

# build_and_push <image:tag> <local-tag> [extra docker build args...]
#
# A multi-platform build cannot be loaded into the local image store, so buildx
# pushes straight to the registry — there is no intermediate `docker tag`. The
# single-arch fallback keeps the old build/tag/push flow and the local tag.
build_and_push() {
  local ref local_tag
  ref="$1"
  local_tag="$2"
  shift 2

  if [ "$HAVE_BUILDX" -eq 1 ]; then
    log "Building $ref for $PLATFORMS and pushing"
    docker buildx build \
      --builder "$BUILDX_BUILDER" \
      --platform "$PLATFORMS" \
      "$@" \
      --tag "$ref" \
      --push \
      .
    return 0
  fi

  log "Building $local_tag for this host's platform only"
  docker build "$@" --tag "$local_tag" .

  log "Tagging $local_tag as $ref"
  docker tag "$local_tag" "$ref"

  log "Pushing $ref"
  docker push "$ref"
}

need docker
need git

if docker buildx version >/dev/null 2>&1; then
  HAVE_BUILDX=1
  ensure_buildx_builder
else
  HAVE_BUILDX=0
  log "WARNING: docker buildx not found — publishing for this host's platform only."
  log "WARNING: pulls from a different architecture will fail with 'no matching manifest'."
fi

GHCR_OWNER="${GHCR_OWNER:-$(github_owner_from_remote || true)}"
GHCR_OWNER="${GHCR_OWNER,,}"
[ -n "$GHCR_OWNER" ] || die "set GHCR_OWNER or configure origin as a GitHub remote"

REGISTRY="${GHCR_REGISTRY:-ghcr.io}"
CONSOLE_IMAGE="${GHCR_CONSOLE_IMAGE:-$REGISTRY/$GHCR_OWNER/overcast}"
SLIM_IMAGE="${GHCR_SLIM_IMAGE:-$REGISTRY/$GHCR_OWNER/overcast-slim}"
TAG="${GHCR_TAG:-dev}"

if [ -n "${GHCR_TOKEN:-}" ]; then
  [ -n "${GHCR_USERNAME:-}" ] || die "GHCR_USERNAME is required when GHCR_TOKEN is set"
  log "Logging in to $REGISTRY as $GHCR_USERNAME"
  printf '%s' "$GHCR_TOKEN" | docker login "$REGISTRY" -u "$GHCR_USERNAME" --password-stdin >/dev/null
else
  need gh
  log "Logging in to $REGISTRY with GitHub CLI auth"
  gh auth status -h github.com >/dev/null
  if ! gh_token_has_scope write:packages; then
    log "Requesting GitHub CLI write:packages scope before building"
    gh auth refresh -h github.com -s write:packages
  fi
  gh_token_has_scope write:packages || die "GitHub CLI token still lacks write:packages scope"
  GHCR_USERNAME="$(gh api user --jq .login)"
  [ -n "$GHCR_USERNAME" ] || die "could not determine GitHub username from gh"
  gh auth token | docker login "$REGISTRY" -u "$GHCR_USERNAME" --password-stdin >/dev/null
fi

build_and_push "$CONSOLE_IMAGE:$TAG" overcast:dev
success "Published $CONSOLE_IMAGE:$TAG"
success "Registry page: $(github_package_url "$CONSOLE_IMAGE")"

build_and_push "$SLIM_IMAGE:$TAG" overcast-slim:dev --target slim
success "Published $SLIM_IMAGE:$TAG"
success "Registry page: $(github_package_url "$SLIM_IMAGE")"

success "Published dev images successfully"
