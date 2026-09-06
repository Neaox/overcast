# shellcheck shell=sh
# go-toolchain.sh — the Go toolchain pin shared by the Docker Go wrappers.
#
# Source this, never execute it. It expects $script_dir (the scripts/
# directory) to be set by the caller, and sets one variable:
#
#   GO_TOOLCHAIN   the value to export as GOTOOLCHAIN inside the container —
#                  go.mod's `toolchain` line verbatim (e.g. "go1.27.0"), or,
#                  if that line is absent, its `go` line with a "go" prefix
#                  added (e.g. "go1.25.0"). Empty if go.mod has neither.
#
# Why this exists: `go test`/`go build` already switch to the toolchain go.mod
# pins on their own (GOTOOLCHAIN=auto is the Go default), because they resolve
# it from the current directory's go.mod. `go run <pkg>@<version>` does not —
# it builds in the invoked module's own build list, so it pins to *that*
# module's go.mod/toolchain line, not this repo's. `make lint-go` runs
# golangci-lint exactly that way, so on an image whose bundled Go is older
# than this repo's pin (see go-image.sh's header for why the image can lag),
# `go run` silently builds golangci-lint with the image's older Go, and
# golangci-lint then refuses to run: "the Go language version (goX.Y) used to
# build golangci-lint is lower than the targeted Go version (Z.W.V)" — Z.W.V
# being *this* go.mod's toolchain line, which golangci-lint reads at runtime.
# Exporting GOTOOLCHAIN explicitly overrides whichever go.mod a `go run`
# invocation would otherwise consult, so every go subcommand in the container
# — including one a `go run`-launched tool spawns internally — targets the
# same pinned toolchain this repo builds and tests against.
#
# A caller-set GOTOOLCHAIN always wins; the wrapper only fills in a default.
# Go downloads a pinned toolchain into GOMODCACHE (golang.org/toolchain@...),
# which the wrappers already mount as a named volume, so the first run pays
# the download once and every later run reuses it.
#
# Consumers: scripts/docker-go.sh. The PowerShell twin is lib/go-toolchain.ps1
# — the two must stay behaviorally identical.

go_mod_toolchain() {
    gomod="$script_dir/../go.mod"
    toolchain=$(sed -n 's/^toolchain[[:space:]]\{1,\}\([^[:space:]]\{1,\}\).*/\1/p' \
        "$gomod" 2>/dev/null | head -n 1)
    if [ -n "$toolchain" ]; then
        printf '%s\n' "$toolchain"
        return
    fi
    goline=$(sed -n 's/^go[[:space:]]\{1,\}\([0-9][^[:space:]]*\).*/\1/p' \
        "$gomod" 2>/dev/null | head -n 1)
    [ -n "$goline" ] && printf 'go%s\n' "$goline"
}

GO_TOOLCHAIN=$(go_mod_toolchain)

# ---- Image-vs-pin drift warning ----------------------------------------------
#
# The image lagging the pin is the case GOTOOLCHAIN above exists to paper
# over (Go just fetches the pinned toolchain on demand) — that direction needs
# no warning. The direction worth flagging is the image running *ahead* of
# go.mod: if .devcontainer/Dockerfile's FROM line gets bumped without a
# matching go.mod toolchain bump, GOTOOLCHAIN=<pin> forces every `go run` to
# fetch and use the *older* pinned toolchain instead of the newer one already
# sitting in the image — silently, and on every invocation. This is a static,
# no-network comparison of version numbers already in hand (the image tag,
# the pinned toolchain) — never a `docker run` just to ask the image its own
# version.
go_toolchain_warn_if_image_ahead() {
    image="$1"
    pin="$2"
    case "$image" in
    golang:[0-9]*) ;;
    *) return 0 ;; # not a stock golang image tag (custom OVERCAST_GO_IMAGE) — nothing to compare
    esac
    image_ver=$(printf '%s\n' "$image" | sed -n 's/^golang:\([0-9]\{1,\}\.[0-9]\{1,\}\).*/\1/p')
    pin_ver=$(printf '%s\n' "$pin" | sed -n 's/^go\([0-9]\{1,\}\.[0-9]\{1,\}\).*/\1/p')
    [ -n "$image_ver" ] && [ -n "$pin_ver" ] || return 0
    [ "$image_ver" != "$pin_ver" ] || return 0

    image_major=${image_ver%%.*}
    image_minor=${image_ver#*.}
    pin_major=${pin_ver%%.*}
    pin_minor=${pin_ver#*.}
    if [ "$image_major" -gt "$pin_major" ] ||
        { [ "$image_major" -eq "$pin_major" ] && [ "$image_minor" -gt "$pin_minor" ]; }; then
        echo "docker-go: warning: image $image ships Go $image_ver, newer than" \
            "the go.mod-pinned toolchain $pin — GOTOOLCHAIN=$pin will force" \
            "every 'go run' to fetch and use the older pin instead. Bump" \
            "go.mod's toolchain line to match." >&2
    fi
}

if [ -n "$GO_TOOLCHAIN" ]; then
    go_toolchain_warn_if_image_ahead "$IMAGE" "$GO_TOOLCHAIN"
fi
