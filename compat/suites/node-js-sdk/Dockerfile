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

# Node 24: the suite has no build step and no TypeScript loader — it runs its
# .ts sources through Node's built-in type stripping, which needs 22.18+ on the
# 22.x line or 23.6+. node:20-alpine cannot run this suite at all.
FROM node:24-alpine

WORKDIR /suite

# Install dependencies first (cached layer).
COPY package.json package-lock.json* ./
RUN npm ci --ignore-scripts

# Copy source.
COPY tsconfig.json run.js ./
COPY src/ ./src/

# Smoke-check the TypeScript compilation (fails fast on type errors).
RUN npx tsc --noEmit

# Default command: run the full suite and emit NDJSON to stdout.
# OVERCAST_ENDPOINT and OVERCAST_DEFAULT_REGION are injected at runtime by the caller
# (docker-compose or CI environment).
CMD ["node", "run.js"]
