import { describe, it, expect, afterEach, vi } from "vitest"

/**
 * The worker script URL must not depend on the route the tab happens to be
 * on.
 *
 * `new SharedWorker(new URL("./event-stream.worker.ts", import.meta.url))` is
 * the shape Vite rewrites at build time into a URL rooted at the app's base,
 * and only that shape: hand a `SharedWorker` a bare string like
 * `"assets/event-stream.worker.js"` and the browser resolves it against the
 * *document*, so it loads on `/` and 404s on every deeper route. #1609 was
 * reported as exactly that failure, so lock the property down here — the
 * argument is a resolved absolute `URL`, and it is the same URL whatever the
 * current path is.
 */

interface Construction {
  url: unknown
  options: unknown
}

function stubSharedWorker(): Construction[] {
  const constructions: Construction[] = []
  class FakeSharedWorker {
    port = {
      addEventListener: () => {},
      start: () => {},
      postMessage: () => {},
    }
    constructor(url: unknown, options: unknown) {
      constructions.push({ url, options })
    }
  }
  vi.stubGlobal("SharedWorker", FakeSharedWorker)
  return constructions
}

/** Loads a fresh copy of the client from `pathname` and subscribes once. */
async function constructWorkerFrom(pathname: string): Promise<Construction> {
  vi.resetModules()
  const constructions = stubSharedWorker()
  window.history.replaceState({}, "", pathname)

  const client = await import("./event-stream.client")
  client.subscribe("/api/events?ep=&region=us-east-1", () => {})

  expect(constructions).toHaveLength(1)
  return constructions[0]
}

describe("event-stream client worker URL", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    window.history.replaceState({}, "", "/")
  })

  it("constructs the SharedWorker from an absolute URL, not a relative string", async () => {
    const { url, options } = await constructWorkerFrom("/")

    expect(url).toBeInstanceOf(URL)
    expect((url as URL).pathname).toMatch(/event-stream\.worker\.[jt]s$/)
    expect(options).toMatchObject({ type: "module", name: "event-stream" })
  })

  it("resolves to the same URL from a deep route as from the root", async () => {
    const root = await constructWorkerFrom("/")
    const deep = await constructWorkerFrom("/sqs/some-queue/messages/")

    expect(String(deep.url)).toBe(String(root.url))
  })

  it("does not resolve the worker against the current document path", async () => {
    const { url } = await constructWorkerFrom("/sqs/some-queue/messages/")

    expect(String(url)).not.toContain("/sqs/")
  })
})
