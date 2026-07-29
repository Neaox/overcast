# Dev container image — used by VS Code Dev Containers and GitHub Codespaces.
# This is NOT the production Dockerfile (that's in the project root).
#
# Contains everything a contributor needs:
#   - Go 1.23 (official image)
#   - Node.js 24 (for Lambda work)
#   - golangci-lint (linter)
#   - Task (cross-platform make alternative)
#   - git, curl, wget, make (standard dev tools)
#   - A non-root `vscode` user matching VS Code's conventions

FROM golang:1.24-bookworm

# Avoid interactive prompts during package installation.
ENV DEBIAN_FRONTEND=noninteractive

# Install system tools.
# curl/wget: for health checks and downloading tools
# make: for Makefile targets
# git: should already be present but explicit is better
# unzip: needed by some go install steps
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    wget \
    make \
    git \
    ripgrep \
    unzip \
    ca-certificates \
    python3 \
    python3-pip \
  && rm -rf /var/lib/apt/lists/*

# Install Node.js 24 (for Lambda emulation work).
# Using NodeSource for a clean, versioned install.
RUN curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
  && apt-get install -y --no-install-recommends nodejs \
  && rm -rf /var/lib/apt/lists/*

# Install pnpm — the package manager for web/ (see web/package.json's
# packageManager field; keep this version in sync with it).
ENV PNPM_VERSION=10.30.1
RUN npm install -g pnpm@${PNPM_VERSION}

# Install golangci-lint — pinned version for reproducibility.
# Check for updates at: https://github.com/golangci/golangci-lint/releases
# v2.x is required: .golangci.yml uses the v2 config schema, which v1 cannot
# parse. Keep in sync with the Makefile and Taskfile.yml.
ENV GOLANGCI_LINT_VERSION=v2.8.0
RUN curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b /usr/local/bin ${GOLANGCI_LINT_VERSION}

# Install Task (cross-platform make alternative, used by Windows contributors).
# https://taskfile.dev/installation/
ENV TASK_VERSION=v3.40.0
RUN sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin ${TASK_VERSION}

# Install mkcert — generates locally-trusted TLS certificates so the Vite dev
# server can run HTTPS/HTTP2 (fixes Firefox's 6-connection HTTP/1.1 limit
# exhausting on long-lived SSE connections).
# https://github.com/FiloSottile/mkcert/releases
ENV MKCERT_VERSION=v1.4.4
RUN curl -sSfL "https://github.com/FiloSottile/mkcert/releases/download/${MKCERT_VERSION}/mkcert-${MKCERT_VERSION}-linux-amd64" \
      -o /usr/local/bin/mkcert \
    && chmod +x /usr/local/bin/mkcert

# Install AWS CLI v2 — pinned version for reproducibility.
# Check for updates at: https://github.com/aws/aws-cli/blob/v2/CHANGELOG.rst
# To install manually inside a running container:
#   curl -sSfL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o /tmp/awscli.zip \
#     && unzip -q /tmp/awscli.zip -d /tmp && sudo /tmp/aws/install && rm -rf /tmp/awscli.zip /tmp/aws
ENV AWS_CLI_VERSION=2.24.21
RUN curl -sSfL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64-${AWS_CLI_VERSION}.zip" \
      -o /tmp/awscli.zip \
    && unzip -q /tmp/awscli.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/awscli.zip /tmp/aws

# Create a non-root `vscode` user — VS Code Dev Containers convention.
# UID 1000 matches the default user on most Linux host systems, which avoids
# file permission issues on mounted volumes.
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=${USER_UID}

RUN groupadd --gid ${USER_GID} ${USERNAME} \
  && useradd --uid ${USER_UID} --gid ${USER_GID} -m ${USERNAME} \
  && apt-get update \
  && apt-get install -y sudo \
  && echo "${USERNAME} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${USERNAME} \
  && chmod 0440 /etc/sudoers.d/${USERNAME} \
  && rm -rf /var/lib/apt/lists/*

# Pre-warm the Go module cache directory so the vscode user can write to it.
# The actual modules are downloaded by postCreateCommand in devcontainer.json.
#
# This MUST be the same path as GOPATH below (/home/vscode/go/pkg/mod), not the
# base image's default /go. Both docker-compose.dev.yml and
# .devcontainer/init-worktree.sh mount a named volume there, and Docker creates
# a volume's mount point owned by root when the path does not already exist in
# the image. The container runs as vscode, so a root-owned mount point fails
# every build with "mkdir /home/vscode/go/pkg/mod/cache: permission denied".
# Creating it here, owned by vscode, is what the volume inherits instead.
#
# /home/vscode/bin exists for the same reason: docker-compose.dev.yml keeps the
# compiled binary on a named volume, and that mount point has to be outside the
# /workspace bind mount — a bind mount hides whatever the image put at its path,
# so a volume nested under it can never inherit image ownership.
RUN mkdir -p /home/${USERNAME}/go/pkg/mod \
             /home/${USERNAME}/bin \
             /home/${USERNAME}/.config/opencode \
             /home/${USERNAME}/.config/gh \
             /home/${USERNAME}/.local/share/opencode \
  && chown -R ${USERNAME}:${USERNAME} \
             /home/${USERNAME}/go \
             /home/${USERNAME}/bin \
             /home/${USERNAME}/.config \
             /home/${USERNAME}/.local

USER ${USERNAME}

# Set GOPATH and add Go/OpenCode binaries to PATH for the vscode user.
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${GOPATH}/bin:/home/${USERNAME}/.opencode/bin:${PATH}
ENV SHELL=/bin/bash

# Install OpenCode from the official installer. The version can be pinned at
# build time with: --build-arg OPENCODE_VERSION=1.0.180
ARG OPENCODE_VERSION=
RUN if [ -n "${OPENCODE_VERSION}" ]; then \
      curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path --version "${OPENCODE_VERSION}"; \
    else \
      curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path; \
    fi \
  && opencode --version

# Default shell for interactive use.
SHELL ["/bin/bash", "-c"]
