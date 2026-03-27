# Dev container image — used by VS Code Dev Containers and GitHub Codespaces.
# This is NOT the production Dockerfile (that's in the project root).
#
# Contains everything a contributor needs:
#   - Go 1.23 (official image)
#   - Node.js 20 (for Lambda work)
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
    unzip \
    ca-certificates \
  && rm -rf /var/lib/apt/lists/*

# Install Node.js 20 (for Lambda emulation work).
# Using NodeSource for a clean, versioned install.
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
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
RUN mkdir -p /go/pkg/mod && chown -R ${USERNAME}:${USERNAME} /go

USER ${USERNAME}

# Set GOPATH and add Go binaries to PATH for the vscode user.
ENV GOPATH=/home/${USERNAME}/go
ENV PATH=${GOPATH}/bin:${PATH}

# Default shell for interactive use.
SHELL ["/bin/bash", "-c"]
