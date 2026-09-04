#!/bin/sh
# Overcast installer for Linux and macOS.
#
#   curl -fsSL https://overcast.sh/install.sh | sh
#   curl -fsSL https://overcast.sh/install.sh | sh -s -- --slim --version v0.0.1-alpha.40
#
# Downloads a release binary for this machine from GitHub Releases, verifies
# it against the release's SHA256SUMS, and puts it in a per-user directory.
# It never asks a question and never runs sudo: every choice is a flag or an
# OVERCAST_INSTALL_* variable, so the same line works in a terminal, in CI and
# in a Dockerfile. Everything is a function and main runs last, so a download
# cut off part-way executes nothing.
#
# The Windows counterpart is install.ps1. Both are served from overcast.sh and
# attached to every release; install/README.md in the Overcast repository is
# the reference for flags, variables and the release-time version bake.

set -eu

# Replaced with the release tag when the script is attached to a release and
# when the website serves it (install/bake.py). Left empty in the repository,
# where "latest" is resolved through the GitHub API instead.
OVERCAST_INSTALL_BAKED_VERSION=""

REPO="overcast-sh/overcast"
BASE_URL="${OVERCAST_INSTALL_BASE_URL:-https://github.com/${REPO}/releases/download}"
RELEASES_API="${OVERCAST_INSTALL_RELEASES_API:-https://api.github.com/repos/${REPO}/releases?per_page=30}"
RELEASES_PAGE="https://github.com/${REPO}/releases"

VERSION="${OVERCAST_INSTALL_VERSION:-}"
INSTALL_DIR="${OVERCAST_INSTALL_DIR:-}"
FLAVOR="${OVERCAST_INSTALL_FLAVOR:-full}"   # full | slim | both
MODIFY_PATH="${OVERCAST_INSTALL_MODIFY_PATH:-0}"
DRY_RUN=0
UNINSTALL=0
RETRIES="${OVERCAST_INSTALL_RETRIES:-4}"

usage() {
  cat <<'EOF'
Overcast installer

  curl -fsSL https://overcast.sh/install.sh | sh
  curl -fsSL https://overcast.sh/install.sh | sh -s -- [options]

Options
  --version <tag>      Release to install (v0.0.1-alpha.40 or 0.0.1-alpha.40).
                       Default: the release this script shipped with, or the
                       newest release when run from a source checkout.
  --slim               Install overcastd, the headless daemon, instead of overcast.
  --both               Install both binaries.
  --dir <path>         Install directory. Default: ~/.local/bin
  --modify-path        Append the install directory to your shell's rc file
                       when it is not already on PATH. Default: print the line.
  --no-modify-path     Never touch rc files (the default, spelled out).
  --dry-run            Print what would be done and exit.
  --uninstall          Remove overcast and overcastd from the install directory.
  -h, --help           This text.

Environment (same meaning as the flag; flags win)
  OVERCAST_INSTALL_VERSION, OVERCAST_INSTALL_FLAVOR (full|slim|both),
  OVERCAST_INSTALL_DIR, OVERCAST_INSTALL_MODIFY_PATH (0|1),
  OVERCAST_INSTALL_OS (linux|darwin), OVERCAST_INSTALL_ARCH (amd64|arm64)

The Windows installer is https://overcast.sh/install.ps1
EOF
}

say() { printf '%s\n' "$*"; }
warn() { printf 'install.sh: %s\n' "$*" >&2; }
die() { printf 'install.sh: error: %s\n' "$*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --version|-v)
        [ $# -ge 2 ] || die "--version needs a value"
        VERSION="$2"; shift 2 ;;
      --version=*) VERSION="${1#--version=}"; shift ;;
      --slim) FLAVOR=slim; shift ;;
      --full) FLAVOR=full; shift ;;
      --both) FLAVOR=both; shift ;;
      --dir|-d)
        [ $# -ge 2 ] || die "--dir needs a value"
        INSTALL_DIR="$2"; shift 2 ;;
      --dir=*) INSTALL_DIR="${1#--dir=}"; shift ;;
      --modify-path) MODIFY_PATH=1; shift ;;
      --no-modify-path) MODIFY_PATH=0; shift ;;
      --dry-run) DRY_RUN=1; shift ;;
      --uninstall) UNINSTALL=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *) usage >&2; die "unknown option: $1" ;;
    esac
  done
  case "$FLAVOR" in full|slim|both) ;; *) die "OVERCAST_INSTALL_FLAVOR must be full, slim or both (got '$FLAVOR')" ;; esac
}

# Binaries to install, one per line, in the order they were built.
binaries() {
  case "$FLAVOR" in
    full) say overcast ;;
    slim) say overcastd ;;
    both) say overcast; say overcastd ;;
  esac
}

detect_platform() {
  os="${OVERCAST_INSTALL_OS:-}"
  arch="${OVERCAST_INSTALL_ARCH:-}"

  if [ -z "$os" ]; then
    case "$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')" in
      linux*) os=linux ;;
      darwin*) os=darwin ;;
      msys*|mingw*|cygwin*)
        die "this looks like Windows. Use the PowerShell installer instead:
  irm https://overcast.sh/install.ps1 | iex" ;;
      *) die "unsupported operating system: $(uname -s). Binaries for other platforms are listed at $RELEASES_PAGE" ;;
    esac
  fi

  if [ -z "$arch" ]; then
    case "$(uname -m 2>/dev/null)" in
      x86_64|amd64) arch=amd64 ;;
      aarch64|arm64) arch=arm64 ;;
      *) die "unsupported architecture: $(uname -m). Overcast ships for amd64 and arm64; see $RELEASES_PAGE" ;;
    esac
    # A shell running under Rosetta reports x86_64 on an Apple Silicon Mac.
    # Ask the kernel; the native binary is the one that belongs here.
    if [ "$os" = darwin ] && [ "$arch" = amd64 ] \
      && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
      arch=arm64
    fi
  fi

  case "$os" in linux|darwin) ;; *) die "OVERCAST_INSTALL_OS must be linux or darwin" ;; esac
  case "$arch" in amd64|arm64) ;; *) die "OVERCAST_INSTALL_ARCH must be amd64 or arm64" ;; esac
  PLATFORM="${os}-${arch}"
}

# download <url> <file>: curl or wget, retried with backoff. A zero-byte body
# counts as a failure — GitHub's asset CDN does hand those back now and then.
# A 404 is final: the release or asset is not there, and waiting will not
# change that.
download() {
  url="$1"; out="$2"; attempt=1
  while :; do
    rm -f "$out"
    code=""
    if have curl; then
      code="$(curl -sSL --retry 2 --retry-delay 1 -o "$out" -w '%{http_code}' "$url" 2>/dev/null || true)"
    elif have wget; then
      wget -q -O "$out" "$url" 2>/dev/null || true
    else
      die "neither curl nor wget is installed"
    fi
    case "$code" in
      404) rm -f "$out"; return 1 ;;
      ""|2*) ;;
      *) rm -f "$out" ;;
    esac
    if [ -s "$out" ]; then
      return 0
    fi
    if [ "$attempt" -ge "$RETRIES" ]; then
      rm -f "$out"
      return 1
    fi
    delay=$((attempt * 2))
    warn "download failed (attempt $attempt of $RETRIES), retrying in ${delay}s: $url"
    sleep "$delay"
    attempt=$((attempt + 1))
  done
}

# Newest release as GitHub lists them. `releases/latest` is not used: it only
# ever answers with a stable release, and returns 404 while every Overcast
# release is a prerelease.
latest_version() {
  tmp="$(mktemp)"
  if ! download "$RELEASES_API" "$tmp"; then
    rm -f "$tmp"
    die "could not fetch the release list from $RELEASES_API
Check your network, or pin a release: --version <tag> (see $RELEASES_PAGE)"
  fi
  # One key per line so the first tag_name wins whether the JSON is pretty
  # printed or compact.
  tag="$(tr ',' '\n' <"$tmp" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  rm -f "$tmp"
  [ -n "$tag" ] || die "no releases found at $RELEASES_API"
  say "$tag"
}

resolve_version() {
  if [ -z "$VERSION" ]; then
    VERSION="$OVERCAST_INSTALL_BAKED_VERSION"
  fi
  if [ -z "$VERSION" ]; then
    VERSION="$(latest_version)"
  fi
  case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
}

sha256_of() {
  if have sha256sum; then
    sha256sum "$1" | cut -d ' ' -f 1
  elif have shasum; then
    shasum -a 256 "$1" | cut -d ' ' -f 1
  elif have openssl; then
    openssl dgst -sha256 "$1" | sed 's/^.* //'
  else
    die "no SHA-256 tool found (sha256sum, shasum or openssl); refusing to install an unverified binary"
  fi
}

# verify <file> <asset> <sums-file>
verify() {
  expected="$(grep -E "^[0-9a-fA-F]{64} [ *]?$2\$" "$3" | head -n 1 | cut -d ' ' -f 1)"
  [ -n "$expected" ] || die "SHA256SUMS for $VERSION has no entry for $2"
  actual="$(sha256_of "$1")"
  if [ "$actual" != "$(say "$expected" | tr '[:upper:]' '[:lower:]')" ]; then
    die "checksum mismatch for $2
  expected: $expected
  actual:   $actual
The download may be corrupt or tampered with. Retry; if it persists, report it at https://github.com/$REPO/issues"
  fi
}

installed_version() {
  # `overcast --version` prints "overcast version <v>". Anything else, or a
  # binary that cannot run here, reads as unknown.
  "$1" --version 2>/dev/null | sed -n 's/^[a-z]* version //p' | head -n 1
}

on_path() {
  case ":${PATH}:" in *":$1:"*) return 0 ;; esac
  return 1
}

shell_name() {
  basename "${SHELL:-sh}"
}

path_line() {
  case "$(shell_name)" in
    fish) say "fish_add_path $1" ;;
    *) say "export PATH=\"$1:\$PATH\"" ;;
  esac
}

rc_file() {
  case "$(shell_name)" in
    zsh) say "${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash)
      if [ "$(uname -s)" = Darwin ] && [ ! -f "$HOME/.bashrc" ]; then
        say "$HOME/.bash_profile"
      else
        say "$HOME/.bashrc"
      fi ;;
    fish) say "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/overcast.fish" ;;
    *) say "$HOME/.profile" ;;
  esac
}

ensure_path() {
  if on_path "$INSTALL_DIR"; then
    return 0
  fi
  line="$(path_line "$INSTALL_DIR")"
  if [ "$MODIFY_PATH" = 1 ]; then
    rc="$(rc_file)"
    if [ "$DRY_RUN" = 1 ]; then
      say "would append to $rc: $line"
      return 0
    fi
    mkdir -p "$(dirname "$rc")"
    if [ -f "$rc" ] && grep -Fq "$line" "$rc"; then
      say "$rc already adds $INSTALL_DIR to PATH; open a new shell to pick it up"
    else
      printf '\n# Added by the Overcast installer\n%s\n' "$line" >>"$rc"
      say "added $INSTALL_DIR to PATH in $rc; open a new shell or run:  $line"
    fi
  else
    say ""
    say "$INSTALL_DIR is not on your PATH. Add it for this shell with:"
    say "  $line"
    say "and put the same line in $(rc_file) to keep it (or re-run with --modify-path)."
  fi
}

uninstall() {
  removed=0
  for name in overcast overcastd; do
    target="$INSTALL_DIR/$name"
    if [ -e "$target" ]; then
      if [ "$DRY_RUN" = 1 ]; then
        say "would remove $target"
      else
        rm -f "$target"
        say "removed $target"
      fi
      removed=1
    fi
  done
  [ "$removed" = 1 ] || say "nothing to remove in $INSTALL_DIR"
  say "PATH entries in shell rc files were left alone."
}

install_one() {
  name="$1"
  asset="${name}-${PLATFORM}"
  url="${BASE_URL}/${VERSION}/${asset}"
  target="${INSTALL_DIR}/${name}"

  current="$(installed_version "$target" 2>/dev/null || true)"
  if [ -n "$current" ] && [ "v$current" = "$VERSION" ]; then
    say "$name $VERSION is already installed at $target"
    return 0
  fi

  if [ "$DRY_RUN" = 1 ]; then
    say "would download $url"
    say "would verify it against ${BASE_URL}/${VERSION}/SHA256SUMS"
    if [ -n "$current" ]; then
      say "would replace $target (v$current) with $VERSION"
    else
      say "would install $target"
    fi
    return 0
  fi

  # Download next to the destination so the final rename stays on one
  # filesystem; cleanup() removes the partial file on any exit.
  PARTIAL="${INSTALL_DIR}/.${name}.download.$$"

  say "downloading $asset $VERSION"
  download "$url" "$PARTIAL" || die "could not download $url
Check that $VERSION exists at $RELEASES_PAGE and ships a $asset asset."
  verify "$PARTIAL" "$asset" "$SUMS"
  chmod 0755 "$PARTIAL"
  mv -f "$PARTIAL" "$target"
  PARTIAL=""

  if [ -n "$current" ]; then
    say "upgraded $name v$current -> $VERSION ($target)"
  else
    say "installed $name $VERSION to $target"
  fi
}

main() {
  parse_args "$@"

  if [ -z "$INSTALL_DIR" ]; then
    INSTALL_DIR="$HOME/.local/bin"
  fi

  if [ "$UNINSTALL" = 1 ]; then
    uninstall
    exit 0
  fi

  detect_platform
  resolve_version

  if [ "$DRY_RUN" = 1 ]; then
    say "platform $PLATFORM, release $VERSION, directory $INSTALL_DIR"
  fi

  if [ "$DRY_RUN" != 1 ]; then
    mkdir -p "$INSTALL_DIR" 2>/dev/null || die "cannot create $INSTALL_DIR; choose another with --dir"
    [ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable; choose another with --dir (this installer never uses sudo)"
    SUMS="$(mktemp)"
    trap cleanup EXIT INT TERM
    download "${BASE_URL}/${VERSION}/SHA256SUMS" "$SUMS" \
      || die "could not download SHA256SUMS for $VERSION from $BASE_URL
Check that the release exists at $RELEASES_PAGE"
  fi

  for name in $(binaries); do
    install_one "$name"
  done

  ensure_path

  first="$(binaries | head -n 1)"
  say ""
  say "Next:"
  say "  $first serve            # run the emulator"
  say "  $first status           # check it"
  say "  eval \"\$($first env)\"    # point AWS tools in this shell at it"
}

SUMS=""
PARTIAL=""
cleanup() {
  [ -n "$SUMS" ] && rm -f "$SUMS"
  [ -n "$PARTIAL" ] && rm -f "$PARTIAL"
  return 0
}

main "$@"
