#!/bin/sh
set -eu

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
  overcast-$safe_name-node-modules:
    name: overcast-$safe_name-node-modules
  overcast-go-mod-cache:
    name: overcast-go-mod-cache
  overcast-gh-config:
    name: overcast-gh-config
EOF
