#!/bin/sh
set -eu

# OVERCAST_DEV_VOLUME_ROOT (optional, per-developer opt-in)
#
# Unset (the default): the dev container uses plain Docker named volumes. Docker
# Desktop keeps those inside its single virtual disk, so if that disk sits on a
# slow disk, node_modules and the Go module cache crawl.
#
# Set to a directory: every named volume below is bound to a subdirectory of it
# instead, so a developer with a small-but-fast disk can place just these volumes
# there. The path must live inside the Linux filesystem the Docker daemon sees —
# a path on a Windows/macOS host drive reaches the daemon through 9p/virtiofs and
# can be slower than the disk you are avoiding.
#
# See CONTRIBUTING.md § "Dev container volumes on a fast disk".
volume_root=${OVERCAST_DEV_VOLUME_ROOT:-}

if [ -n "$volume_root" ]; then
	volume_root_raw=$volume_root

	# Trim trailing slashes so emitted device paths stay tidy.
	while :; do
		case "$volume_root" in
			*/) volume_root=${volume_root%/} ;;
			*) break ;;
		esac
	done

	case "$volume_root" in
		/?*) ;;
		*)
			cat >&2 <<EOF
init-worktree: error: OVERCAST_DEV_VOLUME_ROOT must be an absolute path inside the
  Linux filesystem that the Docker daemon sees, but it is set to:

      $volume_root_raw

  Under WSL2 use something like /mnt/wsl/overcast-volumes. Unset the variable to
  fall back to ordinary Docker named volumes.
  See CONTRIBUTING.md § "Dev container volumes on a fast disk".
EOF
			exit 1
			;;
	esac

	if [ ! -d "$volume_root" ]; then
		cat >&2 <<EOF
init-worktree: error: OVERCAST_DEV_VOLUME_ROOT points at a directory that does not
  exist:

      $volume_root

  Create it first:

      mkdir -p "$volume_root"

  Or unset OVERCAST_DEV_VOLUME_ROOT to fall back to ordinary Docker named volumes.
  See CONTRIBUTING.md § "Dev container volumes on a fast disk".
EOF
		exit 1
	fi

	if [ ! -w "$volume_root" ] || [ ! -x "$volume_root" ]; then
		cat >&2 <<EOF
init-worktree: error: OVERCAST_DEV_VOLUME_ROOT is not writable by $(id -un):

      $volume_root

  Fix the permissions, for example:

      sudo chown -R "\$(id -u):\$(id -g)" "$volume_root"

  Or unset OVERCAST_DEV_VOLUME_ROOT to fall back to ordinary Docker named volumes.
  See CONTRIBUTING.md § "Dev container volumes on a fast disk".
EOF
		exit 1
	fi

	# A host-drive path reaches the Docker daemon through a file-sharing layer
	# (9p / virtiofs / gRPC-FUSE). Its fsync latency is frequently worse than the
	# spinning disk the developer is trying to escape, so refusing to notice
	# would defeat the purpose of the variable.
	shared_mount_hint=
	shared_mount_fix=
	case "$volume_root" in
		/mnt/[A-Za-z] | /mnt/[A-Za-z]/* | /run/desktop/mnt/host/[A-Za-z] | /run/desktop/mnt/host/[A-Za-z]/*)
			shared_mount_hint='a Windows drive shared into WSL2 over 9p'
			shared_mount_fix='Use a directory under /mnt/wsl/ (that is the WSL VM'\''s own filesystem, not a
  shared drive), or attach a second VHDX stored on the fast disk:

      wsl --mount --vhd D:\path\to\overcast-volumes.vhdx --bare'
			;;
		/Users/* | /host_mnt/*)
			shared_mount_hint='a macOS host directory shared into the Docker Desktop VM over virtiofs/gRPC-FUSE'
			shared_mount_fix='Use a path inside the Docker Desktop VM'\''s own filesystem rather than one on
  the macOS host.'
			;;
	esac

	if [ -n "$shared_mount_hint" ]; then
		cat >&2 <<EOF
init-worktree: WARNING: OVERCAST_DEV_VOLUME_ROOT looks like $shared_mount_hint:

      $volume_root

  Bind mounts through that layer often have WORSE fsync latency than a local
  spinning disk, so this will probably make the dev container slower, not faster.

  $shared_mount_fix

  See docs/storage.md § "Put the data directory on an SSD" for the same caveat in
  its user-facing form. Continuing anyway.
EOF
	fi

	printf 'init-worktree: dev container volumes will be bound under %s\n' "$volume_root" >&2
fi

# Emit one entry for the compose top-level `volumes:` map. With
# OVERCAST_DEV_VOLUME_ROOT unset this is byte-for-byte what the script has always
# emitted; with it set the volume additionally gets a local-driver bind to
# <root>/<volume-name>, which is created if missing.
emit_volume() {
	volume_name=$1
	printf '  %s:\n' "$volume_name"
	printf '    name: %s\n' "$volume_name"
	if [ -n "$volume_root" ]; then
		device_path=$volume_root/$volume_name
		mkdir -p "$device_path"
		printf '    driver: local\n'
		printf '    driver_opts:\n'
		printf '      type: none\n'
		printf '      o: bind\n'
		printf '      device: "%s"\n' "$device_path"
	fi
}

workspace_dir=$(pwd)
git_common_dir=$(git rev-parse --git-common-dir)

case "$git_common_dir" in
	/*) git_common_path=$git_common_dir ;;
	*) git_common_path=$workspace_dir/$git_common_dir ;;
esac

if realpath -s "$git_common_path" >/dev/null 2>&1; then
	git_common_path=$(realpath -s "$git_common_path")
else
	git_common_path=$(realpath "$git_common_path")
fi

workspace_name=$(basename "$workspace_dir")
safe_name=$(printf '%s' "$workspace_name" | tr '[:upper:]' '[:lower:]' | tr -c '[:alnum:]_-' '-')
case "$safe_name" in
	[a-z0-9]*) ;;
	*) safe_name=worktree-$safe_name ;;
esac

cat > .devcontainer/.env <<EOF
CURRENT_WORKSPACE_FOLDER=$workspace_dir
GIT_COMMON_DIR=$git_common_path
WORKTREE_SAFE_NAME=$safe_name
EOF

cat > .devcontainer/docker-compose.yaml <<EOF
name: overcast-$safe_name

services:
  devcontainer:
    build:
      context: ..
      dockerfile: .devcontainer/Dockerfile
    command: sleep infinity
    cap_add:
      - SYS_PTRACE
    security_opt:
      - seccomp=unconfined
    volumes:
      - type: bind
        source: ..
        target: /workspace
        consistency: cached
      - type: bind
        source: /var/run/docker.sock
        target: /var/run/docker.sock
      - type: volume
        source: overcast-$safe_name-node-modules
        target: /workspace/web/node_modules
      - type: volume
        source: overcast-go-mod-cache
        target: /home/vscode/go/pkg/mod
      - type: volume
        source: overcast-gh-config
        target: /home/vscode/.config/gh
EOF

case "$git_common_dir" in
	/*)
	cat >> .devcontainer/docker-compose.yaml <<EOF
      - type: bind
        source: "$git_common_path"
        target: "$git_common_path"
        consistency: cached
EOF
		;;
esac

cat >> .devcontainer/docker-compose.yaml <<EOF
    environment:
      CAROOT: "/workspace/.cert"

volumes:
EOF

{
	emit_volume "overcast-$safe_name-node-modules"
	emit_volume overcast-go-mod-cache
	emit_volume overcast-gh-config
} >> .devcontainer/docker-compose.yaml
