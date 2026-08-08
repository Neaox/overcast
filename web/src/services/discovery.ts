/**
 * Service discovery — client-side endpoint resolution.
 *
 * Region fallback chain (highest to lowest priority):
 *   1. sessionStorage — per-tab explicit choice (region selector / ?region= param)
 *   2. localStorage   — last region used in any previous session
 *   3. server         — OVERCAST_DEFAULT_REGION from GET /_/info (seeded at startup)
 *   4. static default — "us-east-1"
 *
 * The baseUrl and label are stored in localStorage and persist across tabs —
 * but only when the user explicitly entered them in the connection dialog.
 * Implicit writes (region seeding at startup, region switches) persist the
 * region only, so a bundled build never pins the endpoint it derived for
 * itself: if the deployment's host or port changes, the next boot follows the
 * freshly injected value instead of dialing a dead stored one.
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
// <script>window.__OVERCAST__ = { apiBaseUrl, region, debug }</script> tag into
// index.html before serving it, so the SPA knows exactly where to reach the
// API — no client-side guessing, no connection dialog.
declare global {
  interface Window {
    __OVERCAST__?: {
      apiBaseUrl?: string
      region?: string
      debug?: boolean
      endpointKnown?: boolean
      inDocker?: boolean
    }
  }
}

export interface ServerInfo {
  region?: string
  account_id?: string
  version?: string
  debug?: boolean
  /** True when OVERCAST_ENFORCE_IAM is on and requests are checked against IAM policies. */
  iam_enforce?: boolean
}

/**
 * True when the UI is embedded into the overcast binary — both the native
 * `overcast serve` build and the Docker console image set `VITE_BUNDLED`.
 * In that mode the endpoint is derived from `window.location`, not the user.
 */
function isBundled(): boolean {
  return import.meta.env.VITE_BUNDLED === "true"
}

function endpointIsUnknown(): boolean {
  if (!isBundled()) return false
  try {
    return window.__OVERCAST__?.endpointKnown === false
  } catch {
    return false
  }
}

function resolveBundledDefault(): EmulatorEndpoint {
  if (typeof window !== "undefined" && window.__OVERCAST__) {
    const overcast = window.__OVERCAST__
    if (overcast.endpointKnown === false) {
      return { baseUrl: "", region: overcast.region ?? "us-east-1" }
    }
    if (overcast.apiBaseUrl) {
      const { apiBaseUrl, region } = overcast
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
  }
  return { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local (4566)" }
}

export const DEFAULT_ENDPOINT: EmulatorEndpoint = isBundled()
  ? resolveBundledDefault()
  : { baseUrl: "http://localhost:4566", region: "us-east-1", label: "Local (4566)" }

const STORAGE_KEY = "overcast:endpoint"
const REGION_SESSION_KEY = "overcast:region"
const REGION_LOCAL_KEY = "overcast:last-region"

/** The shape persisted under {@link STORAGE_KEY} (region is stored separately). */
interface StoredEndpoint {
  baseUrl: string
  label?: string
  /**
   * True when the user typed this endpoint into the connection dialog.
   * Absent on implicit writes from older builds, which persisted every
   * `endpointStore.set` — including automatic region seeding — and thereby
   * pinned bundled deployments to a stale endpoint.
   */
  explicit?: boolean
}

/**
 * Whether a stored endpoint should win over {@link DEFAULT_ENDPOINT} on boot.
 *
 * In bundled builds the server injects a fresh `apiBaseUrl` on every page
 * load, so a stored endpoint only wins when the user explicitly entered it —
 * otherwise a deployment whose host or port changed would keep dialing the
 * dead stored endpoint forever. When the bundled binary cannot determine its
 * own endpoint (`endpointKnown === false`) there is nothing injected to
 * prefer, so any stored endpoint (necessarily user-entered) wins.
 *
 * Non-bundled builds have no injected endpoint, so the stored value always
 * wins, flag or no flag.
 */
function storedEndpointWins(stored: StoredEndpoint): boolean {
  if (!isBundled()) return true
  if (endpointIsUnknown()) return true
  return stored.explicit === true
}

// ─── Resolver interface — swap for service-discovery in future ─────────────

export interface SetEndpointOptions {
  /** The user explicitly entered this endpoint — persist baseUrl and label. */
  explicit?: boolean
}

interface Resolver {
  get(): EmulatorEndpoint
  set(endpoint: EmulatorEndpoint, opts?: SetEndpointOptions): void
  clear(): void
}

class LocalStorageResolver implements Resolver {
  get(): EmulatorEndpoint {
    try {
      const raw = localStorage.getItem(STORAGE_KEY)
      const stored = raw ? (JSON.parse(raw) as StoredEndpoint) : null
      const base =
        stored && storedEndpointWins(stored)
          ? { baseUrl: stored.baseUrl, label: stored.label }
          : DEFAULT_ENDPOINT
      // Priority: per-tab session → last known (localStorage) → static default.
      // Server default (tier 3) is seeded into localStorage at startup in main.tsx
      // so it naturally falls through this chain without special-casing here.
      const sessionRegion = sessionStorage.getItem(REGION_SESSION_KEY)
      const localRegion = localStorage.getItem(REGION_LOCAL_KEY)
      return { ...base, region: sessionRegion ?? localRegion ?? DEFAULT_ENDPOINT.region }
    } catch {
      return DEFAULT_ENDPOINT
    }
  }

  set(endpoint: EmulatorEndpoint, opts?: SetEndpointOptions): void {
    // baseUrl + label persist across tabs, but only for explicit user entries;
    // implicit writes (region seeding, region switches) must not pin the
    // endpoint a bundled build derived for itself. Region is per-tab AND
    // cross-tab either way.
    const { region, baseUrl, label } = endpoint
    if (opts?.explicit) {
      const stored: StoredEndpoint = { baseUrl, label, explicit: true }
      localStorage.setItem(STORAGE_KEY, JSON.stringify(stored))
    }
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
  const data = await fetchServerInfo(baseUrl)
  return data?.region ?? null
}

/**
 * Fetches always-available server metadata from GET /_/info.
 * Returns null on any network or parse error.
 */
export async function fetchServerInfo(baseUrl: string): Promise<ServerInfo | null> {
  try {
    const res = await fetch(`${baseUrl}/_/info`)
    if (!res.ok) return null
    return (await res.json()) as ServerInfo
  } catch {
    return null
  }
}

/**
 * True when the user can choose the endpoint at all.
 *
 * Bundled builds derive the baseUrl from `window.location`, so there is nothing
 * to configure and no way to unconfigure: `isConfigured()` is unconditionally
 * true and clearing storage changes nothing. The one exception is when
 * endpointKnown is explicitly false — the bundled binary cannot determine
 * its own endpoint (e.g. Docker without host networking) and the user must
 * provide one.
 */
export function isEndpointConfigurable(): boolean {
  if (isBundled()) return endpointIsUnknown()
  return true
}

export function isConfigured(): boolean {
  if (isBundled()) {
    if (endpointIsUnknown()) return localStorage.getItem(STORAGE_KEY) !== null
    return true
  }
  return localStorage.getItem(STORAGE_KEY) !== null
}
