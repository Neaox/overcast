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

# ---- Stage 1: Web UI build -------------------------------------------------
# Builds the SPA (Vite). The compiled assets are embedded into the Go binary
# in the next stage — Node.js is NOT present in any runtime image.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web-builder

WORKDIR /web

# Install dependencies first for layer caching.
COPY web/package.json web/package-lock.json* ./
RUN npm ci --ignore-scripts

# Copy web source and build. The generated docs indexes are checked in; this
# Node-only stage intentionally does not regenerate them because Go is not
# installed here.
COPY web/ .
RUN VITE_BUNDLED=true npm run build:bundled

# ---- Stage 2: Go build (conditional — NOSQLITE arg controls slim vs full) --
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS go-builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY embed.go embed_slim.go ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY docs/ ./docs/

# Overlay the built SPA so //go:embed all:web/dist can pick it up.
COPY --from=web-builder /web/dist /src/web/dist

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
FROM alpine:3.20 AS base

RUN apk add --no-cache ca-certificates su-exec

RUN addgroup -S overcast && adduser -S overcast -G overcast
RUN mkdir -p /data && chown overcast:overcast /data

COPY docker/entrypoint-slim.sh /usr/local/bin/entrypoint-slim.sh
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
    OVERCAST_STATE=memory \
    OVERCAST_DATA_DIR=/data \
    OVERCAST_LOG_LEVEL=info \
    OVERCAST_DEFAULT_REGION=us-east-1 \
    OVERCAST_ACCOUNT_ID=000000000000 \
    OVERCAST_DEBUG=false

EXPOSE 4566

HEALTHCHECK --interval=5s --timeout=3s --start-period=2s --retries=3 \
    CMD wget -qO- http://localhost:4566/_health || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint-slim.sh"]

# ---- Stage 4: slim (headless — CI pipelines) -------------------------------
FROM base AS slim

COPY --from=go-builder /overcast /usr/local/bin/overcast

# ---- Stage 5: console (default — with embedded web UI) ---------------------
FROM base

COPY --from=go-builder /overcast /usr/local/bin/overcast

# The Go binary serves the web console on port 4567 (configurable via
# OVERCAST_UI_PORT). The BFF is a pure-Go layer inside the same process.
EXPOSE 4567
