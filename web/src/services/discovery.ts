/**
 * Service discovery — client-side endpoint resolution.
 *
 * Region fallback chain (highest to lowest priority):
 *   1. sessionStorage — per-tab explicit choice (region selector / ?region= param)
 *   2. localStorage   — last region used in any previous session
 *   3. server         — OVERCAST_DEFAULT_REGION from GET /_/info (seeded at startup)
 *   4. static default — "us-east-1"
 *
 * The baseUrl and label are stored in localStorage and persist across tabs.
 * The active region is stored in sessionStorage (per-tab) and also mirrored
 * to localStorage as a cross-tab fallback for fresh sessions.
 *
 * Future modes can be added as new Resolver implementations without
 * touching any consumer code — just swap the active resolver.
 */

export interface EmulatorEndpoint {
  baseUrl: string
  region: string
  label?: string // display name, e.g. "Local (4566)"
}

// When the UI is bundled into the native binary, overcast injects a
// <script>window.__OVERCAST__ = { apiBaseUrl, region }</script> tag into
// index.html before serving it, so the SPA knows exactly where to reach the
// API — no client-side guessing, no connection dialog.
declare global {
  interface Window {
    __OVERCAST__?: { apiBaseUrl?: string; region?: string }
  }
}

function resolveBundledDefault(): EmulatorEndpoint {
  if (typeof window !== "undefined" && window.__OVERCAST__?.apiBaseUrl) {
    const { apiBaseUrl, region } = window.__OVERCAST__
    try {
      const baseUrl = apiBaseUrl.replace(/\/$/, "")
      return {
        baseUrl,
        region: region ?? "us-east-1",
        label: new URL(baseUrl).host,
      }
    } catch {
      // Malformed URL injected by the server — fall through to fallback.
    }
  }
  return { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local (4566)" }
}

export const DEFAULT_ENDPOINT: EmulatorEndpoint =
  import.meta.env.VITE_BUNDLED === "true"
    ? resolveBundledDefault()
    : { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local (4566)" }

const STORAGE_KEY = "overcast:endpoint"
const REGION_SESSION_KEY = "overcast:region"
const REGION_LOCAL_KEY = "overcast:last-region"

// ─── Resolver interface — swap for service-discovery in future ─────────────

interface Resolver {
  get(): EmulatorEndpoint
  set(endpoint: EmulatorEndpoint): void
  clear(): void
}

class LocalStorageResolver implements Resolver {
  get(): EmulatorEndpoint {
    try {
      // Bundled mode: the baseUrl is always derived from window.location so the
      // UI works out of the box wherever overcast is reached from. Stored
      // baseUrls from a prior dev session would be wrong here.
      const bundled = import.meta.env.VITE_BUNDLED === "true"
      const raw = bundled ? null : localStorage.getItem(STORAGE_KEY)
      const stored = raw ? (JSON.parse(raw) as EmulatorEndpoint) : DEFAULT_ENDPOINT
      // Priority: per-tab session → last known (localStorage) → static default.
      // Server default (tier 3) is seeded into localStorage at startup in main.tsx
      // so it naturally falls through this chain without special-casing here.
      const sessionRegion = sessionStorage.getItem(REGION_SESSION_KEY)
      const localRegion = localStorage.getItem(REGION_LOCAL_KEY)
      return { ...stored, region: sessionRegion ?? localRegion ?? DEFAULT_ENDPOINT.region }
    } catch {
      return DEFAULT_ENDPOINT
    }
  }

  set(endpoint: EmulatorEndpoint): void {
    // baseUrl + label persist across tabs; region is per-tab AND cross-tab.
    const { region, ...rest } = endpoint
    localStorage.setItem(STORAGE_KEY, JSON.stringify(rest))
    localStorage.setItem(REGION_LOCAL_KEY, region)
    sessionStorage.setItem(REGION_SESSION_KEY, region)
  }

  clear(): void {
    localStorage.removeItem(STORAGE_KEY)
    localStorage.removeItem(REGION_LOCAL_KEY)
    sessionStorage.removeItem(REGION_SESSION_KEY)
  }
}

// Active resolver — replace this export to switch strategies globally.
export const endpointResolver: Resolver = new LocalStorageResolver()

/**
 * Returns true if a region is already persisted in sessionStorage (explicit
 * per-tab choice) or localStorage (carried over from a previous session).
 * When false, no prior region exists and the server default should be fetched.
 */
export function hasPersistedRegion(): boolean {
  try {
    return (
      sessionStorage.getItem(REGION_SESSION_KEY) !== null ||
      localStorage.getItem(REGION_LOCAL_KEY) !== null
    )
  } catch {
    return false
  }
}

/**
 * Fetches the server's configured region from GET /_/info.
 * Returns null on any network or parse error.
 */
export async function fetchServerRegion(baseUrl: string): Promise<string | null> {
  try {
    const res = await fetch(`${baseUrl}/_/info`)
    if (!res.ok) return null
    const data = (await res.json()) as { region?: string }
    return data.region ?? null
  } catch {
    return null
  }
}

export function isConfigured(): boolean {
  // VITE_BUNDLED is set when the UI is embedded into the overcast binary (both
  // the native `overcast serve` build and the Docker console image). In that
  // mode the endpoint is derived from window.location, so skip the dialog.
  if (import.meta.env.VITE_BUNDLED === "true") return true
  return localStorage.getItem(STORAGE_KEY) !== null
}
