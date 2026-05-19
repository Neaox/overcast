#!/bin/sh
# Entrypoint for the overcast slim image (headless, no web UI).
# Handles Docker socket permissions, runs BOOT init hooks, and drops to the overcast user.

# ── Docker socket permissions ───────────────────────────────────────────
DOCKER_SOCK="${LAMBDA_DOCKER_SOCKET:-/var/run/docker.sock}"
if [ -S "$DOCKER_SOCK" ] && [ "$(id -u)" = "0" ]; then
    SOCK_GID=$(stat -c '%g' "$DOCKER_SOCK")
    if ! getent group "$SOCK_GID" >/dev/null 2>&1; then
        addgroup -g "$SOCK_GID" -S dockerhost
    fi
    SOCK_GROUP=$(getent group "$SOCK_GID" | cut -d: -f1)
    adduser overcast "$SOCK_GROUP" 2>/dev/null || true
fi

# ── Init hooks: BOOT stage ─────────────────────────────────────────────
# BOOT hooks run before overcastd starts, as root. Useful for installing
# packages, adjusting permissions, or other pre-flight setup.
for dir in /etc/localstack/init /etc/overcast/init; do
    hookdir="$dir/boot.d"
    [ -d "$hookdir" ] || continue
    find "$hookdir" -name '*.sh' -type f | sort | while read -r script; do
        if [ -x "$script" ]; then
            echo "init-hook[BOOT]: running $script"
            "$script" || echo "init-hook[BOOT]: $script failed (exit $?)" >&2
        fi
    done
done

# Drop to overcast user.
if [ "$(id -u)" = "0" ]; then
    exec su-exec overcast /usr/local/bin/overcast serve "$@"
else
    exec /usr/local/bin/overcast serve "$@"
fi
