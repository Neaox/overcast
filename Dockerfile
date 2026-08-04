# syntax=docker/dockerfile:1
#
# Multi-platform, multi-target image.
#
# Two images:
#   overcast       — full image with embedded web management console (default)
#   overcast-slim  — Go binary only, no UI, SQLite excluded (for CI pipelines)
#
# Build for the current platform:
#   docker build -t overcast:dev .                                  # console (default)
#   docker build --target slim --build-arg NOSQLITE=1 -t overcast-slim:dev .  # slim
#
# Build for all supported platforms (requires docker buildx):
#   docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/neaox/overcast:latest --push .
#   docker buildx build --platform linux/amd64,linux/arm64 --target slim --build-arg NOSQLITE=1 -t ghcr.io/neaox/overcast-slim:latest --push .
#
# Why this works cross-platform:
#   - CGO_ENABLED=0: pure-Go SQLite (modernc.org/sqlite), no C compiler needed
#   - GOARCH is set automatically by buildx to match the target platform
#   - The golang:alpine builder cross-compiles natively — no QEMU in the build stage
#   - Alpine runtime image has both amd64 and arm64 variants
#
# Base images are pinned by digest, with the tag kept for readability. A tag is
# a moving target: upstream retags alpine:3.20 and the next release ships
# different bytes than the one that was tested, with nothing in the diff to say
# so. The digest makes the image a function of this file, which is what lets
# the build be reproduced and lets CI pull through a registry mirror without
# that changing what ships — a mirror cannot serve different content for a
# digest.
#
# Each digest is the multi-arch index, not a platform manifest, so buildx still
# resolves per-platform underneath. Dependabot keeps them current
# (.github/dependabot.yml); pinning without that would freeze out base-image
# security updates, which is worse than not pinning at all.

# ---- Stage 1: Web UI build --------------------------------------------------
# Builds the SPA (Vite). The compiled assets are embedded into the Go binary
# in the next stage — Node.js is NOT present in any runtime image.
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS web-builder

WORKDIR /web

# corepack resolves the pnpm version pinned in package.json's packageManager field.
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable pnpm

# Install dependencies first for layer caching.
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts

# src/docs-nav.gen.ts is committed, so it arrives with this COPY — no Go
# toolchain or preliminary generation stage is needed to build the SPA.
COPY web/ .
RUN VITE_BUNDLED=true pnpm run build

# ---- Stage 2: Go build (conditional — NOSQLITE arg controls slim vs full) --
FROM --platform=$BUILDPLATFORM golang:1.24-alpine@sha256:8bee1901f1e530bfb4a7850aa7a479d17ae3a18beb6e09064ed54cfd245b7191 AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY embed.go embed_slim.go ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY docs/ ./docs/

# Overlay the built SPA so //go:embed all:web/dist can pick it up. The repo's
# committed web/dist/.gitkeep is .dockerignore'd, so this COPY is the only
# thing that populates the directory — assert it brought a real SPA rather
# than letting a UI-less image ship.
COPY --from=web-builder /web/dist /src/web/dist
RUN test -f /src/web/dist/index.html \
    || (echo "web/dist/index.html missing — the SPA build produced no output" >&2; exit 1)

ARG VERSION=dev
ARG NOSQLITE

# Build a single binary: slim (slim,nosqlite) or full (no tags).
# Output is always /overcast — the runtime stage picks the right one.
RUN if [ -n "$NOSQLITE" ]; then \
        CGO_ENABLED=0 \
        GOOS=${TARGETOS:-linux} \
        GOARCH=${TARGETARCH:-amd64} \
        go build \
            -trimpath \
            -tags slim,nosqlite \
            -ldflags="-w -s -X main.version=${VERSION}" \
            -o /overcast \
            ./cmd/overcast; \
    else \
        CGO_ENABLED=0 \
        GOOS=${TARGETOS:-linux} \
        GOARCH=${TARGETARCH:-amd64} \
        go build \
            -trimpath \
            -ldflags="-w -s -X main.version=${VERSION}" \
            -o /overcast \
            ./cmd/overcast; \
    fi

# ---- Stage 3: shared runtime base ------------------------------------------
# Both slim and console images share the same OS-level setup.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc AS base

RUN apk add --no-cache ca-certificates su-exec

RUN addgroup -S overcast && adduser -S overcast -G overcast
RUN mkdir -p /data && chown overcast:overcast /data

COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
# Back-compat: the script was previously installed as entrypoint-slim.sh;
# keep the old path working for anyone overriding --entrypoint explicitly.
RUN ln -s entrypoint.sh /usr/local/bin/entrypoint-slim.sh
COPY docker/awslocal /usr/local/bin/awslocal

# Init hook directories (LocalStack-compatible + Overcast-native).
RUN mkdir -p /etc/localstack/init/boot.d \
             /etc/localstack/init/start.d \
             /etc/localstack/init/ready.d \
             /etc/localstack/init/shutdown.d \
             /etc/overcast/init/boot.d \
             /etc/overcast/init/start.d \
             /etc/overcast/init/ready.d \
             /etc/overcast/init/shutdown.d

ENV OVERCAST_PORT=4566 \
    OVERCAST_HOST=0.0.0.0 \
    OVERCAST_DATA_DIR=/data \
    OVERCAST_DATA_DIR_SOURCE=image \
    OVERCAST_LOG_LEVEL=info \
    OVERCAST_DEFAULT_REGION=us-east-1 \
    OVERCAST_ACCOUNT_ID=000000000000 \
    OVERCAST_DEBUG=false

# OVERCAST_STATE is intentionally NOT set here — it defaults to "auto" (see
# internal/config/state_auto.go), which resolves to hybrid when a volume or
# bind mount is present at OVERCAST_DATA_DIR (or an existing database is
# found there), and memory otherwise. OVERCAST_DATA_DIR_SOURCE=image above
# marks OVERCAST_DATA_DIR=/data as the image's own baked-in default rather
# than user intent, so `docker run` with no volume mounted still resolves to
# memory (fast, ephemeral — what a volume-less run and most CI usage want),
# while `docker run -v vol:/data ...` resolves to hybrid (persistent) with
# zero configuration. Set OVERCAST_STATE explicitly to override either way.

EXPOSE 4566

# The https fallback keeps the probe working when the daemon serves TLS
# (OVERCAST_TLS=auto or OVERCAST_TLS_CERT/KEY). --no-check-certificate is
# fine here: this is a liveness probe against our own loopback, not a
# trust decision.
HEALTHCHECK --interval=5s --timeout=3s --start-period=2s --retries=3 \
    CMD wget -qO- http://localhost:4566/_health || wget -qO- --no-check-certificate https://localhost:4566/_health || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# ---- Stage 4: slim (headless — CI pipelines) -------------------------------
FROM base AS slim

COPY --from=go-builder /overcast /usr/local/bin/overcast

# ---- Stage 5: console (default — with embedded web UI) ---------------------
FROM base

COPY --from=go-builder /overcast /usr/local/bin/overcast

# The Go binary serves the web console on port 4567 (configurable via
# OVERCAST_UI_PORT). The BFF is a pure-Go layer inside the same process.
EXPOSE 4567
