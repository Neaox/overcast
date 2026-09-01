#!/bin/sh
# run-test-instance.sh — start a throwaway Overcast instance on a FREE pair of
# LOOPBACK ports, never on the user-reserved defaults (4566 API / 4567 web UI).
#
# Agents: AGENTS.md forbids starting instances on 4566/4567 — those belong to
# the user's own running instance. This script picks the first free port pair
# at or above the base (default 4570) and prints both URLs so you can hand the
# user a full, clickable link.
#
# Usage:
#   scripts/run-test-instance.sh                          # memory mode, free ports
#   scripts/run-test-instance.sh --base-port 4600         # scan from 4600 instead
#   scripts/run-test-instance.sh --image overcast:my-branch
#   scripts/run-test-instance.sh --env OVERCAST_STATE=hybrid --data-volume myvol
#   scripts/run-test-instance.sh --mount-docker-socket --no-logs --name shot
#
# Options:
#   --image REF             image to run (default ghcr.io/overcast-sh/overcast:alpha).
#                           Pass the image you built from your own worktree —
#                           `make docker-console` tags it after your branch.
#   --base-port N           start the free-port scan at N (default 4570).
#   --name NAME             name the container, so a later step can stop it.
#   --env KEY=VALUE  (-e)   set one container environment variable. KEY must be
#                           OVERCAST_* or AWS_*. Repeatable.
#   --data-volume NAME      mount the named Docker volume NAME at /data.
#   --mount-docker-socket   bind-mount the host Docker socket (see below).
#   --no-logs               print the endpoints and exit instead of following
#                           logs. What you want when a later step drives the
#                           instance; the container keeps running either way.
#   -h, --help              print this and exit.
#
# Needs docker, and ss(8) or netstat(8) to see which ports are in use. A slim
# Linux image may have neither — `apt-get install iproute2` — and the script
# stops and says so rather than assuming every port is free.
#
# ---------------------------------------------------------------------------
# Why there is no `docker run` passthrough
# ---------------------------------------------------------------------------
# This script is meant to be granted to agents *as a script* — a permission
# entry of `Bash(scripts/run-test-instance.sh:*)` rather than a blanket
# `Bash(docker run:*)`, which would permit any image with any flags,
# `--privileged` included. That grant is only narrower than `docker run` if the
# script cannot be used as a general docker proxy. It used to accept free-form
# arguments after `--` and splice them straight into the command line, which
# made the two grants identical and the narrower one security theatre.
#
# So the options above are the whole surface: an allow-list, not a deny-list.
# An allow-list cannot be incomplete, whereas a deny-list has to stay ahead of
# every flag Docker adds and every spelling of the ones it already has
# (`--privileged=true`, `--net=host`, `-u 0`, `--mount type=bind,...`). The
# deny-list below survives only to make the *message* better: a caller who
# reaches for `--privileged` is told it is refused by policy rather than merely
# unrecognised. Nothing is ever filtered silently — an argument this script
# does not understand stops it, so a caller can never believe something applied
# that did not.
#
# ---------------------------------------------------------------------------
# --mount-docker-socket
# ---------------------------------------------------------------------------
# Appends exactly `-v /var/run/docker.sock:/var/run/docker.sock` and nothing
# else. It is what makes the instance genuinely useful, in two ways:
#
#   * Lambda and ECS need it. Both start containers of their own through the
#     daemon; without the socket neither can run at all.
#   * The web console needs it to find its own API. A container cannot see its
#     own port mapping from the inside, so on a remapped port `deriveAPIBaseURL`
#     (internal/bff/bff.go) cannot tell the SPA where the API is, returns
#     `endpointKnown: false`, and the console shows its *Connect to Overcast*
#     screen. With the socket, `resolvePublishedPort` (cmd/overcast/cmd_serve.go)
#     asks Docker for the container's own bindings, recovers the published
#     port, and the console connects unprompted.
#
# Understand what you are granting: a process that can reach the Docker socket
# can start a privileged container and is therefore root on the host. That is
# true of `make docker-run` too. It is called out here because this flag is the
# one an agent will reach for.
#
# ---------------------------------------------------------------------------
# Loopback publishing
# ---------------------------------------------------------------------------
# Ports are published as `-p 127.0.0.1:<port>:4566`, not the bare
# `-p <port>:4566` this script used to use. A bare mapping binds every
# interface, which puts an unauthenticated emulator — one that will run
# containers for you if the socket is mounted — on whatever network the machine
# is attached to, and trips the Windows Firewall prompt that first exposed it.
# Nothing that talks to a test instance is off-box: the AWS CLI, the SDKs, the
# compat suites and a headless Chrome all run on the host.
#
# Prints the container id, API endpoint, and web UI URL, then follows logs
# until Ctrl-C (the container keeps running; stop with `docker stop <id>`).
set -eu

IMAGE="ghcr.io/overcast-sh/overcast:alpha"
BASE_PORT=4570
NAME=""
DATA_VOLUME=""
MOUNT_SOCKET=0
FOLLOW_LOGS=1

ALLOWED="--image, --base-port, --name, --env KEY=VALUE, --data-volume, --mount-docker-socket, --no-logs"

# Flags that are refused with a reason rather than a shrug. Not a security
# boundary — the allow-list above is — but the difference between "this script
# will not do that" and "you typed something wrong" is worth spelling out, and
# these are the ones worth spelling it out for. Matched after `=value` is
# stripped, so `--network=host` and `--privileged=true` are caught too.
REFUSED="--privileged --cap-add --cap-drop --device --device-cgroup-rule
--security-opt --pid --ipc --uts --userns --cgroupns --cgroup-parent
--network --net --user -u --volume -v --mount --volumes-from --tmpfs
--entrypoint --sysctl --runtime --group-add --add-host --publish -p
--publish-all -P --restart --init --detach -d --rm"

usage() {
    cat <<'EOF'
run-test-instance.sh — start a throwaway Overcast instance on a free pair of
loopback ports, never on 4566/4567 (those belong to the user's own instance).

Usage: scripts/run-test-instance.sh [options]

  --image REF             image to run (default ghcr.io/overcast-sh/overcast:alpha)
  --base-port N           start the free-port scan at N (default 4570)
  --name NAME             name the container so a later step can stop it
  --env KEY=VALUE  (-e)   one container env var; KEY must be OVERCAST_* or
                          AWS_*. Repeatable.
  --data-volume NAME      mount the named Docker volume NAME at /data
  --mount-docker-socket   bind-mount /var/run/docker.sock — what lets the
                          instance run Lambda and ECS, and lets the console
                          discover its own published port and auto-connect
  --no-logs               print the endpoints and exit instead of following logs
  -h, --help              print this

There is deliberately no `docker run` passthrough. The header of this file says
why, and it is worth reading before granting anyone permission to run it.
EOF
}

complain() {
    printf 'run-test-instance.sh: %s\n' "$1" >&2
    shift
    for _line in "$@"; do
        printf '  %s\n' "$_line" >&2
    done
}

# fail: the caller asked for something this script will not do (exit 2).
fail() {
    complain "$@"
    exit 2
}

# die: the caller asked for something reasonable and the environment cannot
# deliver it (exit 1). Kept apart from fail so a wrapper can tell "you typed
# something wrong" from "this machine is missing a tool".
die() {
    complain "$@"
    exit 1
}

# reject names the offending flag and says which kind of "no" this is.
reject() {
    _flag=${1%%=*}
    for _r in $REFUSED; do
        if [ "$_flag" = "$_r" ]; then
            fail "refusing '$_flag'." \
                "This script takes named options only and does not forward arbitrary" \
                "docker run flags: it is granted to agents as a whole script so that the" \
                "grant is narrower than Bash(docker run:*), and a passthrough would erase" \
                "that difference. See the header." \
                "" \
                "Allowed: $ALLOWED" \
                "For the socket use --mount-docker-socket; for a data volume use --data-volume." \
                "Anything else: run docker yourself, and say in the transcript why."
        fi
    done
    fail "unknown option '$1'." "Allowed: $ALLOWED"
}

# POSIX sh has no arrays, so the docker arguments are accumulated onto the tail
# of "$@" itself. A marker is appended first; the parse loop runs until "$1" is
# the marker and pushes each accepted docker argument *past* it with
# `set -- "$@" ...`. One `shift` then drops the marker, leaving "$@" holding
# exactly the docker arguments, still individually quoted — no eval, no word
# splitting, no string of arguments to re-split.
ARG_END='--run-test-instance-end-of-input--'
for _a in "$@"; do
    if [ "$_a" = "$ARG_END" ]; then
        fail "'$ARG_END' is reserved for this script's own argument handling."
    fi
done

# require_value: $1 is the flag, $2 the candidate. The marker standing where a
# value should be means the flag was last on the command line.
require_value() {
    if [ "$#" -lt 2 ] || [ "$2" = "$ARG_END" ]; then
        fail "$1 needs a value."
    fi
}

validate_env() {
    case "$1" in
    *=*) ;;
    *) fail "--env takes KEY=VALUE, got '$1'." ;;
    esac
    _key=${1%%=*}
    case "$_key" in
    OVERCAST_* | AWS_*) ;;
    *) fail "refusing to set '$_key'." \
        "--env is limited to OVERCAST_* and AWS_* so that it configures the emulator" \
        "and nothing else. PATH, LD_PRELOAD and friends are not the emulator's" \
        "configuration surface." ;;
    esac
    case "$_key" in
    *[!A-Z0-9_]*) fail "'$_key' is not a valid environment variable name (A-Z, 0-9, _)." ;;
    esac
}

set -- "$@" "$ARG_END"
while [ "$1" != "$ARG_END" ]; do
    case "$1" in
    --image)
        require_value "$1" "${2-}"
        IMAGE=$2
        shift 2
        ;;
    --base-port)
        require_value "$1" "${2-}"
        BASE_PORT=$2
        shift 2
        ;;
    --name)
        require_value "$1" "${2-}"
        NAME=$2
        shift 2
        ;;
    --data-volume)
        require_value "$1" "${2-}"
        DATA_VOLUME=$2
        shift 2
        ;;
    -e | --env)
        require_value "$1" "${2-}"
        validate_env "$2"
        set -- "$@" -e "$2"
        shift 2
        ;;
    --mount-docker-socket)
        MOUNT_SOCKET=1
        shift
        ;;
    --no-logs)
        FOLLOW_LOGS=0
        shift
        ;;
    -h | --help)
        usage
        exit 0
        ;;
    --)
        fail "'--' is no longer a passthrough for docker run arguments." \
            "It was removed on purpose: see the header." \
            "Allowed: $ALLOWED"
        ;;
    *) reject "$1" ;;
    esac
done
shift

# An image reference that starts with '-' would be read by docker as a flag,
# which is the one way a value could still become one.
case "$IMAGE" in
'' | -*) fail "--image needs an image reference, got '$IMAGE'." ;;
*[!A-Za-z0-9._:/@-]*) fail "'$IMAGE' is not a valid image reference." ;;
esac
case "$BASE_PORT" in
'' | *[!0-9]*) fail "--base-port takes a number, got '$BASE_PORT'." ;;
esac
if [ "$BASE_PORT" -lt 1024 ] || [ "$BASE_PORT" -gt 64000 ]; then
    fail "--base-port must be between 1024 and 64000, got '$BASE_PORT'."
fi

if [ -n "$NAME" ]; then
    case "$NAME" in
    [!A-Za-z0-9]* | *[!A-Za-z0-9_.-]*) fail "'$NAME' is not a valid container name." ;;
    esac
    set -- "$@" --name "$NAME"
fi
if [ -n "$DATA_VOLUME" ]; then
    # A named volume, never a host path: a bind mount is the passthrough this
    # script exists to not have.
    case "$DATA_VOLUME" in
    [!A-Za-z0-9]* | *[!A-Za-z0-9_.-]*) fail "--data-volume takes a named Docker volume, got '$DATA_VOLUME'." ;;
    esac
    set -- "$@" -v "$DATA_VOLUME:/data"
fi
if [ "$MOUNT_SOCKET" -eq 1 ]; then
    set -- "$@" -v /var/run/docker.sock:/var/run/docker.sock
fi

# ---------------------------------------------------------------------------
# Is anything listening?
# ---------------------------------------------------------------------------
# The whole point of the scan is to not take a port somebody else is on, so the
# probe has to be trusted in both directions. Answering "free" wrongly is the
# expensive mistake: it hands out a port already in use, and on the reserved
# pair that means stepping on the user's own instance. So an environment where
# the question cannot be answered stops the script instead of guessing.
#
# ss(8) is asked first. It is iproute2, present on every modern Linux, whereas
# netstat is net-tools — not installed by default on Debian or Ubuntu, which is
# exactly where this runs in CI. netstat then covers macOS and Git Bash on
# Windows, where there is no ss.
#
# What used to be here instead of the stop was `exec 3<>/dev/tcp/127.0.0.1/$1`.
# That is a bash builtin, not a device and not POSIX (shellcheck SC3025), and
# this script is #!/bin/sh: under dash the redirect fails because no such file
# exists, the leading `!` turned that failure into "the port is free", and every
# port reported free — so the scan always returned the base port whether or not
# anything was on it. A silent wrong answer on the branch that only runs where
# netstat is missing, which is to say on most Linux CI images.
PORT_PROBE=
for _tool in ss netstat; do
    if command -v "$_tool" >/dev/null 2>&1; then
        PORT_PROBE=$_tool
        break
    fi
done
[ -n "$PORT_PROBE" ] || die \
    "cannot tell which ports are in use: neither ss(8) nor netstat(8) is on PATH." \
    "Refusing to guess — assuming a port is free is how this script would publish" \
    "onto one that is already taken, the user's own 4566/4567 included." \
    "" \
    "Install one of them (Debian/Ubuntu: apt-get install iproute2, or net-tools)," \
    "or pass --base-port with a port pair you know is free."

# listening_ports: one snapshot of every listening TCP socket, taken once
# rather than per candidate port. Both tools are filtered to their listening
# rows by the same grep — `ss -ltn` prints LISTEN in its state column and
# Windows netstat prints LISTENING, and ss's header row has neither — after
# which the only port on a row that can match a candidate is the local one
# (a listener's peer column is `0.0.0.0:*`, `*.*` or `0.0.0.0:0`).
listening_ports() {
    case "$PORT_PROBE" in
    ss) ss -ltn ;;
    netstat) netstat -an ;;
    esac
}

# A probe that exists but cannot answer — no /proc, a permission error — must
# not read as an empty listener table, which is "everything is free" again.
if ! LISTENERS=$(listening_ports); then
    die "$PORT_PROBE failed, so which ports are in use is unknown." \
        "Its own error is above. Refusing to guess that they are all free."
fi
_grep_rc=0
LISTENERS=$(printf '%s\n' "$LISTENERS" | grep LISTEN) || _grep_rc=$?
# grep exits 1 for "no matches", which on an idle machine is an honest empty
# table, and 2 or more for a failure — which is not the same answer and must
# not be spelled the same way.
if [ "$_grep_rc" -gt 1 ]; then
    die "could not read the listener table $PORT_PROBE printed (grep exited $_grep_rc)." \
        "Refusing to guess that nothing is listening."
fi

# port_free returns success when nothing in the snapshot is listening on the
# port. Addresses end `:PORT` (`0.0.0.0:4566`, `[::]:4566`) everywhere except
# macOS netstat, which writes `127.0.0.1.4566` — hence the leading [.:] — and
# the port must end the field so 4566 does not match 45660.
port_free() {
    ! printf '%s\n' "$LISTENERS" | grep -Eq "[.:]$1([[:space:]]|\$)"
}

# reserved_port: 4566 and 4567 are the user's, in either role. Checking each
# port against both is not pedantry — the guard used to compare the API port
# against 4566 and the UI port against 4567 only, so `--base-port 4565` handed
# the *UI* the user's API port and `--base-port 4567` handed the *API* the
# user's UI port. Both silently break whatever the user is doing, which is the
# one outcome this whole scan exists to avoid.
reserved_port() {
    [ "$1" -eq 4566 ] || [ "$1" -eq 4567 ]
}

api_port=""
p="$BASE_PORT"
while [ "$p" -lt 65000 ]; do
    ui=$((p + 1))
    if ! reserved_port "$p" && ! reserved_port "$ui" && port_free "$p" && port_free "$ui"; then
        api_port="$p"
        ui_port="$ui"
        break
    fi
    p=$((p + 2))
done
[ -n "$api_port" ] || die "no free port pair found from $BASE_PORT."

cid=$(MSYS_NO_PATHCONV=1 docker run -d --rm \
    -p "127.0.0.1:$api_port:4566" -p "127.0.0.1:$ui_port:4567" \
    "$@" \
    "$IMAGE")

echo "container: $cid"
echo "API endpoint: http://localhost:$api_port"
echo "Web UI:       http://localhost:$ui_port"
echo "stop with:    docker stop $(echo "$cid" | cut -c1-12)"
if [ "$FOLLOW_LOGS" -eq 0 ]; then
    exit 0
fi
echo "--- logs (Ctrl-C detaches; container keeps running) ---"
docker logs -f "$cid"
