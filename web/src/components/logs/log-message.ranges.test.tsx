/**
 * The ranges presentation of `LogMessage`: where the CSS Custom Highlight
 * API exists, a syntax-highlighted row is ONE text node — token colour lives
 * in the global highlight registry as ranges, and every lifecycle transition
 * (defer→settle, Format on/off, collapse, unmount) must round-trip leaving
 * the registry holding exactly the live rows' ranges.
 *
 * jsdom has no Highlight API, so it is stubbed and the component re-imported
 * fresh; the sibling log-message.test.tsx exercises the unstubbed jsdom
 * state, which is exactly the markup fallback.
 */
import { act } from "react"
import { cleanup, render } from "@/test/render"
import { tokenizeToRanges } from "@/lib/prism-ranges"
import { resolveTokenColorClass } from "@/lib/highlight-registry"
// Type-only: the component itself is imported fresh per test, after stubbing.
import type { LogMessage as LogMessageStatic } from "./log-message"

class HighlightStub {
  ranges = new Set<Range>()
  add(range: Range) {
    this.ranges.add(range)
  }
  delete(range: Range) {
    this.ranges.delete(range)
  }
}

const registry = () => (CSS as unknown as { highlights: Map<string, HighlightStub> }).highlights

// The registry rebuild is a coalesced microtask (see highlight-registry.ts),
// so counting flushes it first: paint-visible state is post-flush state.
const settledRangeCount = async () => {
  await act(async () => {})
  // Disposal leaves visually-inert garbage for a lazy sweep (see the registry's
  // mutation policy); the painted truth is the post-sweep state. Imported
  // dynamically so it targets the same fresh module instance as the component.
  const { sweepHighlightGarbageForTests } = await import("@/lib/highlight-registry")
  sweepHighlightGarbageForTests()
  return [...registry().values()].reduce((total, highlight) => total + highlight.ranges.size, 0)
}

/** How many ranges the registry should hold for one row showing `text`. */
const expectedRangeCount = (text: string) =>
  tokenizeToRanges(text, "json").filter((r) => resolveTokenColorClass(r.type) !== null).length

const JSON_MESSAGE = '{"level":"error","msg":"boom","attempt":3,"flag":null}'

type LogMessageComponent = typeof LogMessageStatic

let LogMessage: LogMessageComponent

beforeEach(async () => {
  vi.resetModules()
  vi.stubGlobal("Highlight", HighlightStub)
  vi.stubGlobal("CSS", { highlights: new Map<string, HighlightStub>() })
  LogMessage = (await import("./log-message")).LogMessage
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function messageProps(overrides: Partial<Parameters<LogMessageComponent>[0]> = {}) {
  return {
    message: JSON_MESSAGE,
    formatted: false,
    syntaxHighlight: true,
    wrapLines: false,
    filterMatcher: null,
    level: "error" as const,
    ...overrides,
  }
}

describe("LogMessage under the ranges presentation", () => {
  it("renders a highlighted row as one text node with zero token spans, ranges in the registry", async () => {
    const { container } = render(<LogMessage {...messageProps()} />)
    const pre = container.querySelector("pre")
    expect(pre).not.toBeNull()
    expect(pre!.childNodes).toHaveLength(1)
    expect(pre!.firstChild!.nodeType).toBe(Node.TEXT_NODE)
    expect(container.querySelectorAll("span.token")).toHaveLength(0)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_MESSAGE))
  })

  it("applies no ranges to a deferred row, then paints on settle without touching the DOM", async () => {
    const view = render(<LogMessage {...messageProps({ defer: true })} />)
    // Deferred: the text is already there (mounting is cheap either way),
    // colour is not.
    expect(view.container.querySelector("pre")!.textContent).toBe(JSON_MESSAGE)
    expect(await settledRangeCount()).toBe(0)

    const observer = new MutationObserver(() => {})
    observer.observe(view.container, {
      subtree: true,
      childList: true,
      attributes: true,
      characterData: true,
    })
    view.rerender(<LogMessage {...messageProps({ defer: false })} />)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_MESSAGE))
    // The settle that painted the colour produced not one mutation record —
    // the whole point of the ranges backend.
    expect(observer.takeRecords()).toEqual([])
    observer.disconnect()
  })

  it("round-trips Format on/off: the swapped text's ranges replace the old ones exactly", async () => {
    const view = render(<LogMessage {...messageProps()} />)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_MESSAGE))

    const pretty = JSON.stringify(JSON.parse(JSON_MESSAGE), null, 2)
    view.rerender(<LogMessage {...messageProps({ formatted: true })} />)
    expect(view.container.querySelector("pre")!.textContent).toBe(pretty)
    expect(await settledRangeCount()).toBe(expectedRangeCount(pretty))

    view.rerender(<LogMessage {...messageProps({ formatted: false })} />)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_MESSAGE))
  })

  it("round-trips collapse: a collapsed row's ranges leave the registry, expansion restores them", async () => {
    const view = render(<LogMessage {...messageProps()} />)
    view.rerender(<LogMessage {...messageProps({ collapsed: true })} />)
    expect(await settledRangeCount()).toBe(0)
    view.rerender(<LogMessage {...messageProps({ collapsed: false })} />)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_MESSAGE))
  })

  it("removes exactly its own ranges on unmount, leaving other rows' intact", async () => {
    const other = render(<LogMessage {...messageProps({ message: '{"other": true}' })} />)
    const view = render(<LogMessage {...messageProps()} />)
    expect(await settledRangeCount()).toBe(
      expectedRangeCount(JSON_MESSAGE) + expectedRangeCount('{"other": true}'),
    )
    view.unmount()
    expect(await settledRangeCount()).toBe(expectedRangeCount('{"other": true}'))
    other.unmount()
    expect(await settledRangeCount()).toBe(0)
  })

  it("paints an async (worker) result imperatively when the reply lands", async () => {
    // Force the promise path: a fake worker that answers on demand.
    const facade = await import("@/lib/highlight-code")
    // Compact on purpose: LogMessage re-serializes (stringifyJSON) for
    // display, and compact input round-trips to itself.
    const bigDoc = JSON.stringify({ pad: "x".repeat(facade.SYNC_TOKENIZE_MAX_CHARS), ok: true })
    const { packTokenRanges } = await import("@/lib/prism-ranges")
    let deliver: (() => void) | null = null
    vi.stubGlobal(
      "Worker",
      class {
        onmessage: ((event: { data: unknown }) => void) | null = null
        onerror: unknown = null
        postMessage(request: { id: number; text: string; language: string }) {
          deliver = () => {
            this.onmessage?.({
              data: {
                id: request.id,
                ...packTokenRanges(tokenizeToRanges(request.text, request.language)),
              },
            })
          }
        }
        terminate() {}
      },
    )

    const { container } = render(<LogMessage {...messageProps({ message: bigDoc })} />)
    // Mounted, text visible, reply not yet delivered: no colour yet.
    expect(container.querySelector("pre")!.textContent).toBe(bigDoc)
    expect(await settledRangeCount()).toBe(0)

    await act(async () => {
      deliver!()
      // The reply crosses two chained promises (facade cache-fill, then the
      // hook's apply); a macrotask hop outlasts the whole microtask chain.
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(await settledRangeCount()).toBe(expectedRangeCount(bigDoc))
  })
})
