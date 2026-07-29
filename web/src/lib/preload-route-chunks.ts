/**
 * preload-route-chunks — warms every route's code chunk while the browser is
 * idle, so navigating never has to fetch JavaScript over the network.
 *
 * Route components are code-split (autoCodeSplitting in vite.config.ts), so
 * the first visit to a service normally fetches a chunk from the same
 * HTTP/1.1 origin that carries the app-wide SSE stream, Lambda invoke
 * progress streams, S3 transfers and polling queries. Browsers cap HTTP/1.1
 * at six connections per origin: when the emulator is busy those sockets are
 * all taken, the chunk fetch cannot start, and the click looks dropped.
 * Fetching every chunk up front, while the tab is quiet, takes the network
 * out of every later navigation.
 *
 * Only code is warmed, through the router's own chunk loader
 * (`router.loadRouteChunk`, the same call "intent" preloading uses before it
 * runs loaders). Loaders and queries are untouched: data is still preloaded
 * on intent (`defaultPreload` in main.tsx), and firing every route's queries
 * up front would recreate exactly the request storm this exists to avoid.
 */

import type { AnyRoute, AnyRouter } from "@tanstack/react-router"

/**
 * Pacing for browsers without requestIdleCallback (Safari, jsdom). A fixed
 * small delay per chunk keeps the warmup off the critical path without an
 * idle signal to key off.
 */
export const IDLE_FALLBACK_DELAY_MS = 200

/** Run `fn` when the browser reports idle time, or after a short fallback delay. */
function whenIdle(fn: () => void): void {
  if (typeof requestIdleCallback === "function") {
    requestIdleCallback(() => fn())
  } else {
    setTimeout(fn, IDLE_FALLBACK_DELAY_MS)
  }
}

/**
 * Queue every route known to `router` to have its code chunk loaded during
 * browser idle time. Chunks load strictly one at a time, resuming on the next
 * idle period, so the warmup itself never competes with the user for more
 * than one socket. A chunk that fails (dev-server restart, offline) is
 * skipped so the rest still warm; a chunk the router has already loaded is a
 * no-op inside `loadRouteChunk`.
 */
export function preloadRouteChunksWhenIdle(router: AnyRouter): void {
  const queue: AnyRoute[] = Object.values(router.routesById)

  const next = (): void => {
    const route = queue.shift()
    if (!route) return
    void Promise.resolve(router.loadRouteChunk(route))
      .catch(() => undefined)
      .then(() => whenIdle(next))
  }

  whenIdle(next)
}
