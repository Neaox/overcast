#!/bin/sh
# Entrypoint for the overcast console image.
# Starts overcast first (it's the critical process), then launches
# the web management BFF in the background. If the BFF fails, a
# warning is logged but overcast keeps running.

# ── Docker socket permissions ───────────────────────────────────────────
# When the host Docker socket is bind-mounted, it is typically owned by
# root:docker (GID 999 or similar). The overcast user needs access to
# talk to the Docker daemon for Lambda/ECS/RDS/EC2 container ops.
# We detect the socket GID, create a matching group inside the container,
# and add the overcast user to it — then drop privileges via su-exec.
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

# Drop to overcast user (su-exec replaces the current process, like exec).
# If already running as overcast (e.g. no socket), su-exec is a no-op.
RUN_CMD="su-exec overcast"
if [ "$(id -u)" != "0" ]; then
    RUN_CMD=""
fi

# Start overcast in the background so we can launch the BFF after,
# then wait on overcast and forward its exit code.
$RUN_CMD /usr/local/bin/overcast serve "$@" &
OVERCAST_PID=$!

# Start the BFF after overcast is already up. Failures here are non-fatal.
($RUN_CMD node /app/bff/server.mjs 2>&1 || echo "WARNING: web console (BFF) exited — UI unavailable" >&2) &

# Forward signals to overcast so it shuts down cleanly.
trap 'kill -TERM $OVERCAST_PID 2>/dev/null' TERM INT
wait $OVERCAST_PID
exit $?
