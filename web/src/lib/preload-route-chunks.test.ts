import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { AnyRoute, AnyRouter } from "@tanstack/react-router"
import { IDLE_FALLBACK_DELAY_MS, preloadRouteChunksWhenIdle } from "./preload-route-chunks"

function route(id: string): AnyRoute {
  return { id } as unknown as AnyRoute
}

/** A router exposing just the slice preloadRouteChunksWhenIdle uses. */
function fakeRouter(
  ids: string[],
  loadRouteChunk: (r: AnyRoute) => Promise<void> | undefined,
): AnyRouter {
  const routesById: Record<string, AnyRoute> = {}
  for (const id of ids) routesById[id] = route(id)
  return { routesById, loadRouteChunk } as unknown as AnyRouter
}

describe("preloadRouteChunksWhenIdle", () => {
  beforeEach(() => {
    vi.useFakeTimers()
    // jsdom has no requestIdleCallback; make the fallback path explicit so the
    // test still means the same thing if jsdom ever grows one.
    vi.stubGlobal("requestIdleCallback", undefined)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it("loads every route's chunk, one per idle slice, none before idle", async () => {
    const loaded: string[] = []
    const router = fakeRouter(["__root__", "/s3", "/sqs"], (r) => {
      loaded.push(r.id)
      return Promise.resolve()
    })

    preloadRouteChunksWhenIdle(router)
    expect(loaded).toEqual([])

    await vi.advanceTimersByTimeAsync(IDLE_FALLBACK_DELAY_MS)
    expect(loaded).toEqual(["__root__"])

    await vi.advanceTimersByTimeAsync(IDLE_FALLBACK_DELAY_MS)
    await vi.advanceTimersByTimeAsync(IDLE_FALLBACK_DELAY_MS)
    expect(loaded).toEqual(["__root__", "/s3", "/sqs"])
  })

  it("skips a chunk that fails to load and still warms the rest", async () => {
    const loaded: string[] = []
    const router = fakeRouter(["/bad", "/good"], (r) => {
      if (r.id === "/bad") return Promise.reject(new Error("dev server restarted"))
      loaded.push(r.id)
      return Promise.resolve()
    })

    preloadRouteChunksWhenIdle(router)
    await vi.advanceTimersByTimeAsync(IDLE_FALLBACK_DELAY_MS * 2)

    expect(loaded).toEqual(["/good"])
  })

  it("tolerates routes with no chunk to load", async () => {
    const loaded: string[] = []
    const router = fakeRouter(["/inline", "/split"], (r) => {
      if (r.id === "/inline") return undefined // nothing lazy about this route
      loaded.push(r.id)
      return Promise.resolve()
    })

    preloadRouteChunksWhenIdle(router)
    await vi.advanceTimersByTimeAsync(IDLE_FALLBACK_DELAY_MS * 2)

    expect(loaded).toEqual(["/split"])
  })

  it("uses requestIdleCallback when the browser provides it", () => {
    const idle: (() => void)[] = []
    vi.stubGlobal("requestIdleCallback", (cb: IdleRequestCallback) => {
      idle.push(() => cb({} as IdleDeadline))
      return idle.length
    })

    const loaded: string[] = []
    const router = fakeRouter(["/s3"], (r) => {
      loaded.push(r.id)
      return Promise.resolve()
    })

    preloadRouteChunksWhenIdle(router)
    expect(loaded).toEqual([])
    expect(idle).toHaveLength(1)

    idle.shift()!()
    expect(loaded).toEqual(["/s3"])
  })
})
