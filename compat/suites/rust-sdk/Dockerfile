# syntax=docker/dockerfile:1
FROM rust:1.91-slim-bookworm AS build

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config libssl-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY rust-sdk/Cargo.toml rust-sdk/Cargo.lock* ./

RUN mkdir -p src && echo 'fn main() {}' > src/main.rs && cargo build && rm -rf src

COPY rust-sdk/src/ ./src/
RUN touch src/main.rs && cargo build

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /build/target/debug/rust-sdk-compat /app/rust-sdk-compat

COPY registry.json /registry.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["/app/rust-sdk-compat"]
