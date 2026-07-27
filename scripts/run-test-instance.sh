#!/bin/sh
# run-test-instance.sh — start a throwaway Overcast instance on FREE ports for
# testing, never on the user-reserved defaults (4566 API / 4567 web UI).
#
# Agents: AGENTS.md forbids starting instances on 4566/4567 — those belong to
# the user's own running instance. This script picks the first free port pair
# at or above the base (default 4570) and prints both URLs so you can hand the
# user a full, clickable link.
#
# Usage:
#   scripts/run-test-instance.sh                       # memory mode, free ports
#   scripts/run-test-instance.sh --base-port 4600      # scan from 4600 instead
#   scripts/run-test-instance.sh --image IMG           # a specific built image
#   scripts/run-test-instance.sh -- -e OVERCAST_STATE=hybrid -v myvol:/data
#                                                      # everything after -- is
#                                                      # passed to `docker run`
#
# The instance runs ghcr.io/neaox/overcast:alpha by default — pass --image to
# test a locally built one. Prints the container id, API endpoint, and web UI
# URL, then follows logs until Ctrl-C (the container keeps running; stop with
# `docker stop <id>`).
set -eu

IMAGE="ghcr.io/neaox/overcast:alpha"
BASE_PORT=4570

while [ "$#" -gt 0 ]; do
    case "$1" in
        --image) IMAGE="$2"; shift 2 ;;
        --base-port) BASE_PORT="$2"; shift 2 ;;
        --) shift; break ;;
        *) echo "unknown flag: $1 (pass docker-run args after --)" >&2; exit 2 ;;
    esac
done

# port_free returns success when nothing is listening on the port. Works in
# Git Bash / Linux / macOS without extra tools: try to bind with a tiny
# busybox-less trick via docker (portable) is overkill — netstat is universal
# enough on the platforms this repo supports.
port_free() {
    if command -v netstat >/dev/null 2>&1; then
        ! netstat -an 2>/dev/null | grep -Eq "[.:]$1[[:space:]].*LISTEN"
    else
        # Fall back to attempting a TCP connect: connect success = taken.
        ! (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null
    fi
}

api_port=""
p="$BASE_PORT"
while [ "$p" -lt 65000 ]; do
    ui=$((p + 1))
    # Never pick the user-reserved pair, even if the scan base is moved.
    if [ "$p" -ne 4566 ] && [ "$ui" -ne 4567 ] && port_free "$p" && port_free "$ui"; then
        api_port="$p"
        ui_port="$ui"
        break
    fi
    p=$((p + 2))
done
[ -n "$api_port" ] || { echo "no free port pair found from $BASE_PORT" >&2; exit 1; }

cid=$(MSYS_NO_PATHCONV=1 docker run -d --rm \
    -p "$api_port:4566" -p "$ui_port:4567" \
    "$@" \
    "$IMAGE")

echo "container: $cid"
echo "API endpoint: http://localhost:$api_port"
echo "Web UI:       http://localhost:$ui_port"
echo "stop with:    docker stop $(echo "$cid" | cut -c1-12)"
echo "--- logs (Ctrl-C detaches; container keeps running) ---"
docker logs -f "$cid"
