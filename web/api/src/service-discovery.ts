/**
 * Service discovery client.
 *
 * In local mode the emulator endpoint is read from session storage (set by the
 * connection config UI). In future modes (e.g. a hosted console backed by a
 * real endpoint registry) this module is the single place that changes.
 *
 * The API server receives endpoint config via a header set by the browser
 * client, so all route handlers remain adapter-agnostic.
 */

export interface EmulatorEndpoint {
  /** e.g. "http://localhost:4566" */
  baseUrl: string
  region: string
}

/** Header the browser sends with every /api/* request. */
export const ENDPOINT_HEADER = "x-overcast-endpoint"
export const REGION_HEADER = "x-overcast-region"

const apiPort = process.env.OVERCAST_PORT || "4566"

/** Default endpoint — constructed from env vars or falls back to localhost:4566. */
export const DEFAULT_ENDPOINT: EmulatorEndpoint = {
  baseUrl: process.env.EMULATOR_ENDPOINT || `http://localhost:${apiPort}`,
  region: process.env.OVERCAST_DEFAULT_REGION || "us-east-1",
}

/**
 * Resolve the emulator endpoint from a Hono request context.
 * Falls back to DEFAULT_ENDPOINT if headers are absent.
 */
export function resolveEndpoint(headers: Record<string, string | undefined>): EmulatorEndpoint {
  const baseUrl = headers[ENDPOINT_HEADER] ?? DEFAULT_ENDPOINT.baseUrl
  const region = headers[REGION_HEADER] ?? DEFAULT_ENDPOINT.region
  return { baseUrl: baseUrl.replace(/\/$/, ""), region }
}

/**
 * Base URL of the Go BFF (internal/bff), the single implementation of every
 * `/api/*` route (see docs/plans/dev-bff-consolidation.md, B1). It is the web
 * UI port `overcast serve` starts alongside the emulator's API port — 4567 by
 * default, matching cmd/overcast/cmd_serve.go's defaultUIPort — so this dev
 * server needs a locally running, built overcast binary to answer `/api/*`
 * requests, exactly as the shipped console does.
 *
 * `GO_BFF_ENDPOINT` overrides the whole URL; `OVERCAST_UI_PORT` (same name as
 * the Go flag/env var) overrides only the port against localhost, for the
 * common case of a UI port picked to avoid colliding with another worktree's
 * server (see docs/dev/development-setup.md's parallel-agent guidance).
 */
export const GO_BFF_ENDPOINT = (
  process.env.GO_BFF_ENDPOINT || `http://localhost:${process.env.OVERCAST_UI_PORT || "4567"}`
).replace(/\/$/, "")
