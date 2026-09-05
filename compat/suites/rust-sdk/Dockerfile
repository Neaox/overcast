# syntax=docker/dockerfile:1
FROM rust:1.94.1-slim-bookworm AS build

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config libssl-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY rust-sdk/Cargo.toml rust-sdk/Cargo.lock* ./

RUN mkdir -p src && echo 'fn main() {}' > src/main.rs && cargo build && rm -rf src

COPY rust-sdk/src/ ./src/
# The registration test in main.rs resolves the real per-service impl maps
# against the real registry via the same OVERCAST_REGISTRY_PATH default
# ("../registry.json", relative to WORKDIR) the binary itself falls back to,
# so the build stage needs that file one directory above WORKDIR too — the
# same file the final stage below already ships to the image root.
COPY registry.json /registry.json
# `cargo test` beside the build, for the same reason java-sdk's Dockerfile runs
# `mvn package`: src/registry.rs carries the unit tests that pin impl-key
# resolution, and a regression there changes what a compat run *reports*
# without failing it. They need no emulator, and the dependency layer above is
# already compiled, so this costs one extra compile of this crate alone —
# cheaper than standing a Rust toolchain up in test.yml to say the same thing.
RUN touch src/main.rs && cargo test && cargo build

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /build/target/debug/rust-sdk-compat /app/rust-sdk-compat

# Both halves of the registry: the loader resolves registry.generated.json as
# the sibling of registry.json, so shipping only the hand-written half would
# leave the image silently ignoring every generated group.
COPY registry.json /registry.json
COPY registry.generated.json /registry.generated.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["/app/rust-sdk-compat"]
