/**
 * The shared highlight component under the ranges presentation: where the CSS
 * Custom Highlight API exists, a highlighted block is ONE text node — token
 * colour lives in the global highlight registry as ranges, applied with zero
 * DOM mutation, and removed exactly when the block unmounts or its text
 * changes.
 *
 * jsdom has no Highlight API, so it is stubbed and the component re-imported
 * fresh (the same pattern as log-message.ranges.test.tsx); the sibling
 * highlighted-code.test.tsx exercises the unstubbed jsdom state, which is
 * exactly the markup fallback.
 */
import { act } from "react"
import { cleanup, render } from "@/test/render"
import { tokenizeToRanges } from "@/lib/prism-ranges"
import { resolveTokenColorClass } from "@/lib/highlight-registry"
// Type-only: the component itself is imported fresh per test, after stubbing.
import type { HighlightedCode as HighlightedCodeStatic } from "./highlighted-code"

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

/** How many ranges the registry should hold for one block showing `text`. */
const expectedRangeCount = (text: string, language: string) =>
  tokenizeToRanges(text, language).filter((r) => resolveTokenColorClass(r.type) !== null).length

const JSON_TEXT = '{"level":"error","msg":"boom","attempt":3,"flag":null}'

let HighlightedCode: typeof HighlightedCodeStatic

beforeEach(async () => {
  vi.resetModules()
  vi.stubGlobal("Highlight", HighlightStub)
  vi.stubGlobal("CSS", { highlights: new Map<string, HighlightStub>() })
  HighlightedCode = (await import("./highlighted-code")).HighlightedCode
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("HighlightedCode under the ranges presentation", () => {
  it("renders one text node with zero token spans, ranges in the registry", async () => {
    const { container } = render(<HighlightedCode text={JSON_TEXT} language="json" />)
    const pre = container.querySelector("pre")
    expect(pre).not.toBeNull()
    expect(pre!.childNodes).toHaveLength(1)
    expect(pre!.firstChild!.nodeType).toBe(Node.TEXT_NODE)
    expect(container.querySelectorAll("span.token")).toHaveLength(0)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_TEXT, "json"))
  })

  it("paints the S3 preview's other languages — markup, css, javascript — as ranges", async () => {
    // The adoption this component exists for: languages beyond json ride the
    // same kernel. Each is a fresh mount so the counts add up one at a time.
    for (const [language, text] of [
      ["markup", '<root>\n  <child attr="v">text</child>\n</root>'],
      ["css", "body { color: red; }"],
      ["javascript", "const x = { ok: true }"],
    ] as const) {
      const expected = expectedRangeCount(text, language)
      expect(expected).toBeGreaterThan(0)
      const view = render(<HighlightedCode text={text} language={language} />)
      expect(view.container.querySelectorAll("span.token")).toHaveLength(0)
      expect(await settledRangeCount()).toBe(expected)
      view.unmount()
      expect(await settledRangeCount()).toBe(0)
    }
  })

  it("applies no ranges for language null — plain policy stays plain", async () => {
    const { container } = render(<HighlightedCode text={JSON_TEXT} language={null} />)
    expect(container.querySelector("pre")!.textContent).toBe(JSON_TEXT)
    expect(await settledRangeCount()).toBe(0)
  })

  it("applies no ranges to a deferred block, then paints on settle without touching the DOM", async () => {
    const view = render(<HighlightedCode text={JSON_TEXT} language="json" defer />)
    expect(view.container.querySelector("pre")!.textContent).toBe(JSON_TEXT)
    expect(await settledRangeCount()).toBe(0)

    const observer = new MutationObserver(() => {})
    observer.observe(view.container, {
      subtree: true,
      childList: true,
      attributes: true,
      characterData: true,
    })
    view.rerender(<HighlightedCode text={JSON_TEXT} language="json" defer={false} />)
    expect(await settledRangeCount()).toBe(expectedRangeCount(JSON_TEXT, "json"))
    // The settle that painted the colour produced not one mutation record —
    // the whole point of the ranges backend.
    expect(observer.takeRecords()).toEqual([])
    observer.disconnect()
  })

  it("swaps ranges when the text changes, and removes its own on unmount", async () => {
    const other = render(<HighlightedCode text='{"other": true}' language="json" />)
    const view = render(<HighlightedCode text={JSON_TEXT} language="json" />)
    expect(await settledRangeCount()).toBe(
      expectedRangeCount(JSON_TEXT, "json") + expectedRangeCount('{"other": true}', "json"),
    )
    const pretty = JSON.stringify(JSON.parse(JSON_TEXT), null, 2)
    view.rerender(<HighlightedCode text={pretty} language="json" />)
    expect(await settledRangeCount()).toBe(
      expectedRangeCount(pretty, "json") + expectedRangeCount('{"other": true}', "json"),
    )
    view.unmount()
    expect(await settledRangeCount()).toBe(expectedRangeCount('{"other": true}', "json"))
    other.unmount()
    expect(await settledRangeCount()).toBe(0)
  })
})
