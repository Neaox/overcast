/**
 * Service discovery — client-side endpoint resolution.
 *
 * Currently: user-configured endpoint stored in sessionStorage.
 *
 * Future modes can be added as new Resolver implementations without
 * touching any consumer code — just swap the active resolver.
 */

export interface EmulatorEndpoint {
  baseUrl: string
  region: string
  label?: string // display name, e.g. "Local (4566)"
}

export const DEFAULT_ENDPOINT: EmulatorEndpoint = {
  baseUrl: "http://localhost:4566",
  region: "us-east-1",
  label: "Local (4566)",
}

const SESSION_KEY = "overcast:endpoint"

// ─── Resolver interface — swap for service-discovery in future ─────────────

interface Resolver {
  get(): EmulatorEndpoint
  set(endpoint: EmulatorEndpoint): void
  clear(): void
}

class SessionStorageResolver implements Resolver {
  get(): EmulatorEndpoint {
    try {
      const raw = sessionStorage.getItem(SESSION_KEY)
      return raw ? (JSON.parse(raw) as EmulatorEndpoint) : DEFAULT_ENDPOINT
    } catch {
      return DEFAULT_ENDPOINT
    }
  }

  set(endpoint: EmulatorEndpoint): void {
    sessionStorage.setItem(SESSION_KEY, JSON.stringify(endpoint))
  }

  clear(): void {
    sessionStorage.removeItem(SESSION_KEY)
  }
}

// Active resolver — replace this export to switch strategies globally.
export const endpointResolver: Resolver = new SessionStorageResolver()

export function isConfigured(): boolean {
  return sessionStorage.getItem(SESSION_KEY) !== null
}
