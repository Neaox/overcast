import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

/**
 * Endpoint resolution order.
 *
 * `DEFAULT_ENDPOINT` is computed at module load from `VITE_BUNDLED` and
 * `window.__OVERCAST__`, so every test re-imports the module after arranging
 * its environment.
 */

const STORAGE_KEY = "overcast:endpoint"
const REGION_SESSION_KEY = "overcast:region"
const REGION_LOCAL_KEY = "overcast:last-region"

const INJECTED = "http://host.example:9999"
const STORED = "http://stored.example:4566"

async function loadDiscovery() {
  vi.resetModules()
  return await import("./discovery")
}

function storeEndpoint(baseUrl: string, extra: Record<string, unknown> = {}) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify({ baseUrl, label: "stored", ...extra }))
}

beforeEach(() => {
  localStorage.clear()
  sessionStorage.clear()
})

afterEach(() => {
  vi.unstubAllEnvs()
  delete window.__OVERCAST__
})

describe("endpointResolver.get — dev (non-bundled) build", () => {
  it("falls back to the static default when nothing is stored", async () => {
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe("http://localhost:4566")
  })

  it("prefers a stored endpoint marked explicit", async () => {
    storeEndpoint(STORED, { explicit: true })
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(STORED)
  })

  it("still honours a legacy stored endpoint without the explicit flag", async () => {
    storeEndpoint(STORED)
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(STORED)
  })
})

describe("endpointResolver.get — bundled build with an injected endpoint", () => {
  beforeEach(() => {
    vi.stubEnv("VITE_BUNDLED", "true")
    window.__OVERCAST__ = { apiBaseUrl: INJECTED, region: "eu-west-1" }
  })

  it("uses the injected apiBaseUrl when nothing is stored", async () => {
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(INJECTED)
  })

  it("prefers the injected apiBaseUrl over an implicitly stored endpoint", async () => {
    // The regression: automatic region seeding used to persist the derived
    // baseUrl, and on the next boot the stale stored value beat the freshly
    // injected one — the UI kept dialing a dead endpoint after the
    // deployment's host or port changed.
    storeEndpoint(STORED)
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(INJECTED)
  })

  it("lets an explicit user-entered endpoint override the injected one", async () => {
    storeEndpoint(STORED, { explicit: true })
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(STORED)
  })
})

describe("endpointResolver.get — bundled build that cannot determine its endpoint", () => {
  beforeEach(() => {
    vi.stubEnv("VITE_BUNDLED", "true")
    window.__OVERCAST__ = { endpointKnown: false, region: "eu-west-1" }
  })

  it("has no endpoint until the user provides one", async () => {
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe("")
  })

  it("uses the stored endpoint even without the explicit flag — there is no injected endpoint to prefer", async () => {
    storeEndpoint(STORED)
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().baseUrl).toBe(STORED)
  })
})

describe("endpointResolver.set", () => {
  it("persists baseUrl and label with the explicit marker on an explicit set", async () => {
    const { endpointResolver, isConfigured } = await loadDiscovery()
    endpointResolver.set(
      { baseUrl: STORED, region: "eu-west-1", label: "mine" },
      { explicit: true },
    )
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!)).toEqual({
      baseUrl: STORED,
      label: "mine",
      explicit: true,
    })
    expect(isConfigured()).toBe(true)
  })

  it("does not persist the endpoint on an implicit set (region seeding / switching)", async () => {
    const { endpointResolver, isConfigured } = await loadDiscovery()
    endpointResolver.set({ baseUrl: STORED, region: "eu-west-1" })
    expect(localStorage.getItem(STORAGE_KEY)).toBeNull()
    expect(isConfigured()).toBe(false)
  })

  it("persists the region on both explicit and implicit sets", async () => {
    const { endpointResolver } = await loadDiscovery()
    endpointResolver.set({ baseUrl: STORED, region: "eu-west-1" })
    expect(sessionStorage.getItem(REGION_SESSION_KEY)).toBe("eu-west-1")
    expect(localStorage.getItem(REGION_LOCAL_KEY)).toBe("eu-west-1")
  })

  it("leaves an explicit stored endpoint intact across an implicit region switch", async () => {
    const { endpointResolver } = await loadDiscovery()
    endpointResolver.set(
      { baseUrl: STORED, region: "us-east-1", label: "mine" },
      { explicit: true },
    )
    endpointResolver.set({ baseUrl: STORED, region: "ap-southeast-2" })
    const resolved = endpointResolver.get()
    expect(resolved.baseUrl).toBe(STORED)
    expect(resolved.label).toBe("mine")
    expect(resolved.region).toBe("ap-southeast-2")
  })
})

describe("region resolution order", () => {
  it("prefers the per-tab session region over the cross-tab one", async () => {
    sessionStorage.setItem(REGION_SESSION_KEY, "eu-central-1")
    localStorage.setItem(REGION_LOCAL_KEY, "us-west-2")
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().region).toBe("eu-central-1")
  })

  it("falls back to the cross-tab region, then the default", async () => {
    localStorage.setItem(REGION_LOCAL_KEY, "us-west-2")
    const { endpointResolver } = await loadDiscovery()
    expect(endpointResolver.get().region).toBe("us-west-2")

    localStorage.removeItem(REGION_LOCAL_KEY)
    expect(endpointResolver.get().region).toBe("us-east-1")
  })
})
