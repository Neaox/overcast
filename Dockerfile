# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32
#
# The frontend is digest-pinned like every FROM below it. It was the one
# reference in this file that was not, and it is fetched before any of them —
# so a Docker Hub hiccup resolving it failed the build before a single layer
# was read, which is how `main` went red on an afternoon when nothing about the
# image had changed. A digest is immutable and can be served from any mirror;
# a floating tag has to be resolved from the registry every time.
#
# Dependabot advances the FROM digests below, but its docker ecosystem reads
# FROM lines and this is a comment — so if it is still on this digest a release
# or two from now, bump it by hand:
#   docker buildx imagetools inspect docker/dockerfile:1 --format '{{.Manifest.Digest}}'
#
# Multi-platform, multi-target image.
#
# Two images:
#   overcast       — full image with embedded web management console (default)
#   overcast-slim  — Go binary only, no UI, SQLite excluded (for CI pipelines)
#
# Build for the current platform — prefer the targets, which tag the image after
# the current branch so parallel worktrees cannot build into one name:
#   make docker-console        # console (default), overcast:<sanitised branch>
#   make docker-slim           # slim, overcast-slim:<sanitised branch>
#   make docker-clean          # remove this branch's pair when you are done
#
# By hand, if you must:
#   docker build -t overcast:dev .                          # console (default)
#   docker build --target slim -t overcast-slim:dev .       # slim
#
# Build for all supported platforms (requires docker buildx):
#   docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/neaox/overcast:latest --push .
#   docker buildx build --platform linux/amd64,linux/arm64 --target slim -t ghcr.io/neaox/overcast-slim:latest --push .
#
# `--target slim` is the whole instruction: the target selects a builder stage
# that compiles `-tags slim,nosqlite`, so there is no second thing to remember.
# There used to be — a NOSQLITE build arg that a single builder stage branched
# on — and every caller that passed `--target slim` and forgot it silently got
# the full binary in the slim image. The release workflow was one of them, so
# `overcast-slim` shipped with the SPA, SQLite and /_overcast/mcp for several releases
# (#798). A build arg that must agree with the target is a build arg that will
# eventually disagree with it; the flavour is now a property of the target
# alone, and each builder asserts what it produced (see the RUN steps below).
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
# by the console builder — Node.js is NOT present in any runtime image, and
# the slim build never reaches this stage at all.
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:c610fcdfb1d5b4740dd70c284ed3cb16bb857e0f7166196e36a5501df7a3aa32 AS web-builder

WORKDIR /web

# corepack resolves the pnpm version pinned in package.json's packageManager
# field. It shipped bundled with Node through v24 but is gone from newer
# distributions (Node 26 LTS included, due October 2026) — install it as its
# own npm package instead, which works the same regardless of what Node
# bundles. See https://github.com/Neaox/overcast/issues/558.
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN npm install -g corepack@latest && corepack enable pnpm

# Install dependencies first for layer caching.
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile --ignore-scripts

# src/docs-nav.gen.ts is committed, so it arrives with this COPY — no Go
# toolchain or preliminary generation stage is needed to build the SPA.
COPY web/ .
RUN VITE_BUNDLED=true pnpm run build

# ---- Stage 2: Go sources (shared by both builders) -------------------------
# Everything up to (but not including) the SPA overlay and the compile itself.
# Both builders start here, so the module download and source COPYs are one
# cached layer set rather than two.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS go-src

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY embed.go embed_slim.go ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY docs/ ./docs/

# ---- Stage 3: Go build, slim flavour ---------------------------------------
# No SPA overlay and no dependency on web-builder at all: `-tags slim` compiles
# embed_slim.go, whose WebDistFS is an empty embed.FS. BuildKit therefore skips
# the whole Node stage for a `--target slim` build.
FROM go-src AS go-builder-slim

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    go build \
        -trimpath \
        -tags slim,nosqlite \
        -ldflags="-w -s -X main.version=${VERSION}" \
        -o /overcast \
        ./cmd/overcast

# Assert the binary really is slim, by looking at the binary rather than
# trusting the command line that produced it — the failure this guards against
# was precisely a build that looked right and compiled the wrong tags.
#
# Three markers, one per thing `slim,nosqlite` is supposed to remove: the
# embedded SPA's file names (//go:embed all:web/dist, !slim only), the runtime
# MCP route (registered in internal/router/mcp_routes.go, !slim only), and the
# SQLite driver (!nosqlite only). A cross-compiled binary is just bytes to
# grep, so this works for every --platform without QEMU.
RUN for marker in 'web/dist/' '/_overcast/mcp' 'modernc.org/sqlite'; do \
        if grep -q -F "$marker" /overcast; then \
            echo "slim binary contains '$marker' — it was not built with -tags slim,nosqlite" >&2; \
            exit 1; \
        fi; \
    done \
    && echo "slim binary verified: no SPA, no /_overcast/mcp, no SQLite"

# ---- Stage 4: Go build, console flavour ------------------------------------
FROM go-src AS go-builder-console

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

# Overlay the built SPA so //go:embed all:web/dist can pick it up. The repo's
# committed web/dist/.gitkeep is .dockerignore'd, so this COPY is the only
# thing that populates the directory — assert it brought a real SPA rather
# than letting a UI-less image ship.
COPY --from=web-builder /web/dist /src/web/dist
RUN test -f /src/web/dist/index.html \
    || (echo "web/dist/index.html missing — the SPA build produced no output" >&2; exit 1)

RUN CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    go build \
        -trimpath \
        -ldflags="-w -s -X main.version=${VERSION}" \
        -o /overcast \
        ./cmd/overcast

# The mirror image of the slim assertion, so a future attempt to make slim
# genuinely slim cannot do it by making console slim too.
RUN for marker in 'web/dist/index.html' '/_overcast/mcp'; do \
        if ! grep -q -F "$marker" /overcast; then \
            echo "console binary is missing '$marker' — a console build must not use -tags slim" >&2; \
            exit 1; \
        fi; \
    done \
    && echo "console binary verified: SPA and /_overcast/mcp present"

# ---- Stage 5: shared runtime base ------------------------------------------
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
    OVERCAST_LISTEN=0.0.0.0 \
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
#
# This stage is shared, and the paragraph above describes the CONSOLE image
# only. The slim stage below carries a `nosqlite` binary, and hybrid/persistent
# need SQLite: there, auto short-circuits to memory whatever is mounted, and
# hybrid/persistent refuse to start. wal is the durable backend that still
# works. Do not add an OVERCAST_STATE default here to "fix" that — a default
# would have to differ per stage, and the auto resolver already reports the
# reason at startup. See docs/storage.md § Builds without SQLite.

EXPOSE 4566

# Covers both modes; --no-check-certificate is fine here: this is a liveness
# probe against our own loopback, not a trust decision.
#
# https is tried FIRST, and the order is the whole point. A plain-HTTP probe
# against a TLS listener makes the daemon log
# "http: TLS handshake error ...: client sent an HTTP request to an HTTPS
# server" — once every interval, forever, for a perfectly healthy container,
# which buries the handshake errors that do mean something. The reverse costs
# nothing: an https probe against a plain-HTTP listener is refused client-side
# (wrong version number) and the server logs nothing at all.
HEALTHCHECK --interval=5s --timeout=3s --start-period=2s --retries=3 \
    CMD wget -qO- --no-check-certificate https://localhost:4566/_overcast/health || wget -qO- http://localhost:4566/_overcast/health || exit 1

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]

# ---- Stage 6: slim (headless — CI pipelines) -------------------------------
FROM base AS slim

COPY --from=go-builder-slim /overcast /usr/local/bin/overcast

# ---- Stage 7: console (default — with embedded web UI) ---------------------
FROM base

COPY --from=go-builder-console /overcast /usr/local/bin/overcast

# The Go binary serves the web console on port 4567 (configurable via
# OVERCAST_UI_PORT). The BFF is a pure-Go layer inside the same process.
EXPOSE 4567
