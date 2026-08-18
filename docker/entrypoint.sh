#!/bin/sh
# Entrypoint for both overcast images (slim and console — set in the shared
# `base` stage of the Dockerfile). The console's web UI/BFF is a pure-Go layer
# inside the same overcast binary, so both images start the same single process.
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

# ── Shared CA: make it readable by the overcast user ───────────────────
# A CA shared from the host (OVERCAST_CA_DIR, mounted :ro so the container
# cannot rewrite the machine's trust anchor) arrives with the HOST's
# ownership, and private keys are 0600. Uids do not line up across a bind
# mount — on Docker Desktop the files land owned by root — so the
# unprivileged overcast user we are about to drop to typically cannot read
# them, and the daemon dies with "permission denied" on rootCA-key.pem.
#
# We are still root here, so copy the material somewhere the daemon can read
# it and point the daemon there. The mount itself is never touched or
# written to. The copy being ephemeral is correct rather than a compromise:
# the durable original lives on the host, which is the entire point of
# sharing it — the container is only borrowing a signing key for its lifetime.
#
# Guarded on the CA actually existing. An empty or not-yet-populated mount
# must fall through untouched, or the daemon would mint a brand-new CA into
# a directory that dies with the container — exactly the failure this whole
# mechanism exists to prevent.
if [ "$(id -u)" = "0" ] && [ -n "$OVERCAST_CA_DIR" ] && [ -f "$OVERCAST_CA_DIR/rootCA.pem" ]; then
    if ! su-exec overcast test -r "$OVERCAST_CA_DIR/rootCA-key.pem"; then
        RUNTIME_CA_DIR=/var/lib/overcast/ca
        mkdir -p "$RUNTIME_CA_DIR"
        cp -a "$OVERCAST_CA_DIR"/. "$RUNTIME_CA_DIR"/
        chown -R overcast:overcast "$RUNTIME_CA_DIR"
        chmod 700 "$RUNTIME_CA_DIR"
        echo "entrypoint: CA at $OVERCAST_CA_DIR is not readable by the overcast user (host ownership across a bind mount) — serving from a private copy at $RUNTIME_CA_DIR; the mount is left untouched"
        OVERCAST_CA_DIR="$RUNTIME_CA_DIR"
        export OVERCAST_CA_DIR
    fi
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
