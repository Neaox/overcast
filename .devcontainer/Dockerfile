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

# Install golangci-lint — pinned version for reproducibility.
# Check for updates at: https://github.com/golangci/golangci-lint/releases
ENV GOLANGCI_LINT_VERSION=v1.62.2
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
RUN mkdir -p /go/pkg/mod \
             /home/${USERNAME}/.config/opencode \
             /home/${USERNAME}/.config/gh \
             /home/${USERNAME}/.local/share/opencode \
  && chown -R ${USERNAME}:${USERNAME} /go /home/${USERNAME}/.config /home/${USERNAME}/.local

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
