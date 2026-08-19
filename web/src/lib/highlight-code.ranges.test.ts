/**
 * The facade's detection matrix and worker plumbing. jsdom has neither
 * `CSS.highlights` nor `Worker`, so every capability is stubbed per test and
 * the module re-imported fresh — module state (caches, the worker handle) is
 * part of what's under test.
 */
import { packTokenRanges, tokenizeToRanges } from "./prism-ranges"
import type { HighlightWorkRequest } from "./highlight-worker"

class HighlightStub {
  ranges = new Set<unknown>()
  add(range: unknown) {
    this.ranges.add(range)
  }
  delete(range: unknown) {
    this.ranges.delete(range)
  }
}

function stubHighlightApi() {
  vi.stubGlobal("Highlight", HighlightStub)
  vi.stubGlobal("CSS", { highlights: new Map<string, HighlightStub>() })
}

class WorkerStub {
  static instances: WorkerStub[] = []
  posted: HighlightWorkRequest[] = []
  onmessage: ((event: { data: unknown }) => void) | null = null
  onerror: ((event: unknown) => void) | null = null
  terminated = false
  url: URL
  options: WorkerOptions | undefined
  constructor(url: URL, options?: WorkerOptions) {
    this.url = url
    this.options = options
    WorkerStub.instances.push(this)
  }
  postMessage(message: HighlightWorkRequest) {
    this.posted.push(message)
  }
  terminate() {
    this.terminated = true
  }
  /** Deliver the packed reply for one posted request, as the real worker would. */
  reply(request: HighlightWorkRequest) {
    this.onmessage?.({
      data: {
        id: request.id,
        ...packTokenRanges(tokenizeToRanges(request.text, request.language)),
      },
    })
  }
}

async function importFacade() {
  return await import("./highlight-code")
}

// Comfortably above SYNC_TOKENIZE_MAX_CHARS, so the worker path engages.
function largeJson(seed: string): string {
  const items = Array.from({ length: 400 }, (_, i) => ({ seed, index: i, ok: i % 2 === 0 }))
  return JSON.stringify({ items }, null, 2)
}

beforeEach(() => {
  vi.resetModules()
  WorkerStub.instances = []
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("detection matrix", () => {
  it("selects markup when CSS.highlights is missing (jsdom's natural state)", async () => {
    const facade = await importFacade()
    expect(facade.supportsHighlightRanges()).toBe(false)
    expect(facade.highlightPresentation()).toBe("markup")
  })

  it("selects ranges when CSS.highlights and Highlight exist, and registers the token highlights once", async () => {
    stubHighlightApi()
    const facade = await importFacade()
    expect(facade.highlightPresentation()).toBe("ranges")
    const registry = (CSS as unknown as { highlights: Map<string, unknown> }).highlights
    expect(registry.get("overcast-token-string")).toBeInstanceOf(HighlightStub)
    expect(registry.get("overcast-token-number")).toBeInstanceOf(HighlightStub)
  })

  it("selects markup when CSS exists but the Highlight constructor is absent", async () => {
    vi.stubGlobal("CSS", { highlights: new Map() })
    const facade = await importFacade()
    expect(facade.highlightPresentation()).toBe("markup")
  })
})

describe("requestTokenRanges", () => {
  it("tokenizes small documents synchronously and caches the identical array", async () => {
    const facade = await importFacade()
    const text = '{"a": 1, "b": null}'
    const first = facade.requestTokenRanges(text, "json")
    expect(Array.isArray(first)).toBe(true)
    expect(first).toEqual(tokenizeToRanges(text, "json"))
    // Identity, not equality: a stable value is what lets an effect keyed on
    // it skip re-application.
    expect(facade.requestTokenRanges(text, "json")).toBe(first)
  })

  it("tokenizes large documents synchronously when no Worker exists", async () => {
    const facade = await importFacade()
    const text = largeJson("no-worker")
    expect(text.length).toBeGreaterThan(facade.SYNC_TOKENIZE_MAX_CHARS)
    const result = facade.requestTokenRanges(text, "json")
    expect(Array.isArray(result)).toBe(true)
  })

  it("routes large documents through one persistent worker and coalesces duplicate texts", async () => {
    vi.stubGlobal("Worker", WorkerStub)
    const facade = await importFacade()
    const text = largeJson("coalesce")

    const first = facade.requestTokenRanges(text, "json")
    const second = facade.requestTokenRanges(text, "json")
    expect(first).toBeInstanceOf(Promise)
    // Two callers, one in-flight promise, one message on the wire.
    expect(second).toBe(first)
    expect(WorkerStub.instances).toHaveLength(1)
    const worker = WorkerStub.instances[0]
    expect(worker.posted).toHaveLength(1)
    expect(worker.options?.type).toBe("module")

    worker.reply(worker.posted[0])
    const ranges = await first
    expect(ranges).toEqual(tokenizeToRanges(text, "json"))

    // Resolved replies populate the cache: the next ask is synchronous and
    // does not message the worker again.
    expect(facade.requestTokenRanges(text, "json")).toBe(ranges)
    expect(worker.posted).toHaveLength(1)

    // A different large document reuses the same worker — persistent, not
    // per-call.
    void facade.requestTokenRanges(largeJson("second-doc"), "json")
    expect(WorkerStub.instances).toHaveLength(1)
  })

  it("keeps small documents off the worker entirely", async () => {
    vi.stubGlobal("Worker", WorkerStub)
    const facade = await importFacade()
    const result = facade.requestTokenRanges('{"small": true}', "json")
    expect(Array.isArray(result)).toBe(true)
    expect(WorkerStub.instances).toHaveLength(0)
  })

  it("falls back to synchronous tokenization when the worker errors, resolving whatever was pending", async () => {
    vi.stubGlobal("Worker", WorkerStub)
    const facade = await importFacade()
    const text = largeJson("error-path")

    const pending = facade.requestTokenRanges(text, "json")
    expect(pending).toBeInstanceOf(Promise)
    const worker = WorkerStub.instances[0]
    worker.onerror?.({ message: "worker exploded" })

    // The stranded request resolves correctly anyway…
    expect(await pending).toEqual(tokenizeToRanges(text, "json"))
    expect(worker.terminated).toBe(true)

    // …and the session stops using workers: the next large ask is synchronous
    // on the same (dead) session, with no new worker constructed.
    const after = facade.requestTokenRanges(largeJson("after-error"), "json")
    expect(Array.isArray(after)).toBe(true)
    expect(WorkerStub.instances).toHaveLength(1)
  })

  it("declines to construct a worker whose constructor throws, without losing the result", async () => {
    vi.stubGlobal(
      "Worker",
      class {
        constructor() {
          throw new Error("CSP says no")
        }
      },
    )
    const facade = await importFacade()
    const result = facade.requestTokenRanges(largeJson("csp"), "json")
    expect(Array.isArray(result)).toBe(true)
  })
})
