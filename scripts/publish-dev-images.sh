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

set -euo pipefail

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

need docker
need git
need make

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

log "Building console image with make docker-console"
make docker-console

log "Tagging overcast:dev as $CONSOLE_IMAGE:$TAG"
docker tag overcast:dev "$CONSOLE_IMAGE:$TAG"

log "Pushing $CONSOLE_IMAGE:$TAG"
docker push "$CONSOLE_IMAGE:$TAG"
success "Published $CONSOLE_IMAGE:$TAG"
success "Registry page: $(github_package_url "$CONSOLE_IMAGE")"

log "Building slim image with make docker-slim"
make docker-slim

log "Tagging overcast-slim:dev as $SLIM_IMAGE:$TAG"
docker tag overcast-slim:dev "$SLIM_IMAGE:$TAG"

log "Pushing $SLIM_IMAGE:$TAG"
docker push "$SLIM_IMAGE:$TAG"
success "Published $SLIM_IMAGE:$TAG"
success "Registry page: $(github_package_url "$SLIM_IMAGE")"

success "Published dev images successfully"
