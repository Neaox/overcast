# syntax=docker/dockerfile:1
#
# Multi-platform image for Mac (ARM64 + AMD64), Linux (AMD64 + ARM64), Windows (AMD64).
#
# Build for the current platform:
#   docker build -t overcast:dev .
#
# Build for all supported platforms (requires docker buildx):
#   docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/your-org/overcast:latest --push .
#
# Why this works cross-platform:
#   - CGO_ENABLED=0: pure-Go SQLite (modernc.org/sqlite), no C compiler needed
#   - GOARCH is set automatically by buildx to match the target platform
#   - The golang:alpine builder cross-compiles natively — no QEMU in the build stage
#   - Alpine runtime image has both amd64 and arm64 variants

# ---- Stage 1: build --------------------------------------------------------
# BUILDPLATFORM = the machine running the build (e.g. linux/arm64 on Apple Silicon)
# TARGETPLATFORM = the platform we're building FOR (e.g. linux/amd64 for CI servers)
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

# Receive the target OS and architecture from buildx.
# These are passed automatically when using --platform.
ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Cache the module download layer separately from source.
# This layer only rebuilds when go.mod or go.sum change.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Cross-compile for the target platform.
# CGO_ENABLED=0 is what makes cross-compilation trivial — no C toolchain needed.
RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    go build \
        -trimpath \
        -ldflags="-w -s" \
        -o /overcast \
        ./cmd/overcast

# ---- Stage 2: runtime image ------------------------------------------------
# Alpine has native amd64 and arm64 variants — Docker pulls the right one automatically.
FROM alpine:3.20

# Node.js is required for Lambda Node runtime emulation.
# It has native Alpine packages for both amd64 and arm64 — no emulation.
# To build a smaller image without Lambda support:
#   docker build --build-arg INCLUDE_NODE=false ...
# and set OVERCAST_SERVICES=s3,sqs,dynamodb,sns at runtime.
ARG INCLUDE_NODE=true
RUN if [ "$INCLUDE_NODE" = "true" ]; then \
        apk add --no-cache nodejs ca-certificates; \
    else \
        apk add --no-cache ca-certificates; \
    fi

# Non-root user — never run server processes as root.
RUN addgroup -S overcast && adduser -S overcast -G overcast

# Persistence directory for SQLite state backend.
RUN mkdir -p /data && chown overcast:overcast /data

COPY --from=builder /overcast /usr/local/bin/overcast

USER overcast

# Default configuration — all overridable at runtime via environment variables.
# See README.md for the full configuration reference.
ENV OVERCAST_PORT=4566 \
    OVERCAST_HOST=0.0.0.0 \
    OVERCAST_STATE=memory \
    OVERCAST_DATA_DIR=/data \
    OVERCAST_LOG_LEVEL=info \
    OVERCAST_REGION=us-east-1 \
    OVERCAST_ACCOUNT_ID=000000000000 \
    OVERCAST_DEBUG=false

EXPOSE 4566

# Health check — used by Docker Compose, Kubernetes, and ECS.
# wget is included in Alpine by default; curl is not.
HEALTHCHECK --interval=5s --timeout=3s --start-period=2s --retries=3 \
    CMD wget -qO- http://localhost:4566/_health || exit 1

ENTRYPOINT ["/usr/local/bin/overcast"]
