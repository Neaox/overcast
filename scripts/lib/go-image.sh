# shellcheck shell=sh
# go-image.sh — the Go image shared by every Go-in-Docker entry point.
#
# Source this, never execute it. It expects $script_dir (the scripts/
# directory) to be set by the caller, and sets one variable:
#
#   GO_IMAGE   the image to `docker run`: OVERCAST_GO_IMAGE when set, else the
#              image .devcontainer/Dockerfile builds FROM, else the literal
#              fallback below.
#
# Reading the devcontainer's Dockerfile keeps the wrappers and the devcontainer
# on the same Go by construction. They drifted once: go.mod moved to `go 1.25.0`
# and the devcontainer to golang:1.26-bookworm while the wrappers still said
# golang:1.24-bookworm, so every `docker-go.sh test` died on the spot with
# "go: go.mod requires go >= 1.25.0 (running go 1.24.13; GOTOOLCHAIN=local)".
# The fallback only matters when that file is missing or its FROM line is not
# a plain image reference (an ${ARG}, say); keep it equal to the real line so a
# fallback run behaves like a normal one.
#
# Consumers: scripts/docker-go.sh, scripts/go.sh. The PowerShell twin is
# lib/go-image.ps1 — the two must stay behaviorally identical.

GO_IMAGE_FALLBACK="golang:1.26-bookworm"

# devcontainer_go_image — the image named by the first FROM line of
# .devcontainer/Dockerfile, minus any `--platform=...` flags before it and any
# `AS stage` after it. Prints nothing if the file is unreadable, has no FROM,
# or the reference is not literal.
devcontainer_go_image() {
    image=$(sed -n 's/^FROM[[:space:]]\{1,\}\(--[^[:space:]]*[[:space:]]\{1,\}\)*\([^[:space:]]\{1,\}\).*/\2/p' \
        "$script_dir/../.devcontainer/Dockerfile" 2>/dev/null | head -n 1)
    case "$image" in
    *'$'*) ;;
    *) printf '%s\n' "$image" ;;
    esac
}

if [ -n "${OVERCAST_GO_IMAGE:-}" ]; then
    GO_IMAGE="$OVERCAST_GO_IMAGE"
else
    GO_IMAGE=$(devcontainer_go_image)
    [ -n "$GO_IMAGE" ] || GO_IMAGE="$GO_IMAGE_FALLBACK"
fi
