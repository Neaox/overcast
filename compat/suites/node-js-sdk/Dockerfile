# compat/suites/node-js-sdk/Dockerfile
#
# Builds a self-contained image that runs the node-js-sdk compatibility suite.
# The image is used by compat/docker-compose.yml so the suite can run in CI
# without any local Node.js installation.
#
# Build standalone:
#   docker build -t overcast-compat-node-js-sdk compat/suites/node-js-sdk
#
# Run against a local Overcast instance:
#   docker run --rm --network host \
#     -e OVERCAST_ENDPOINT=http://localhost:4566 \
#     overcast-compat-node-js-sdk

FROM node:20-alpine

WORKDIR /suite

# Install dependencies first (cached layer).
COPY package.json package-lock.json* ./
RUN npm ci --ignore-scripts

# Copy source.
COPY tsconfig.json ./
COPY src/ ./src/

# Smoke-check the TypeScript compilation (fails fast on type errors).
RUN npx tsc --noEmit

# Default command: run the full suite and emit NDJSON to stdout.
# OVERCAST_ENDPOINT and OVERCAST_DEFAULT_REGION are injected at runtime by the caller
# (docker-compose or CI environment).
CMD ["node", "--import", "tsx/esm", "src/runner.ts"]
