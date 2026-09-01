import Prism from "@/lib/prism"
import {
  grammarTokenClasses,
  packTokenRanges,
  tokenizeToRanges,
  unpackTokenRanges,
} from "./prism-ranges"

const NESTED_JSON = JSON.stringify(
  {
    level: "error",
    message: 'boom: "quoted" \\ escape',
    attempt: 3,
    durationMs: 142.75,
    success: false,
    context: {
      region: "ap-southeast-2",
      tags: ["alpha", "beta", 42, true, null],
      inner: { deep: { value: -1.5e3 } },
    },
    memo: null,
  },
  null,
  2,
)

/**
 * The markup backend's view of the same tokenization: walk Prism's HTML and
 * collect (text, classes) per top-level piece — token spans carry their
 * classes, bare text nodes are the un-tokenized gaps.
 */
function markupPieces(text: string): { text: string; type: string | null }[] {
  const host = document.createElement("div")
  host.innerHTML = Prism.highlight(text, Prism.languages.json, "json")
  return [...host.childNodes].map((node) => {
    if (node.nodeType === Node.TEXT_NODE) return { text: node.textContent ?? "", type: null }
    const el = node as HTMLElement
    return { text: el.textContent, type: el.className.replace(/^token /, "") }
  })
}

describe("tokenizeToRanges", () => {
  it("tiles nested JSON exactly: sorted, no gaps unaccounted, no overlaps", () => {
    const ranges = tokenizeToRanges(NESTED_JSON, "json")
    expect(ranges.length).toBeGreaterThan(0)

    let cursor = 0
    for (const range of ranges) {
      // Sorted and non-overlapping: each range starts at or after the
      // previous one ended, and spans forward.
      expect(range.start).toBeGreaterThanOrEqual(cursor)
      expect(range.end).toBeGreaterThan(range.start)
      cursor = range.end
    }
    expect(cursor).toBeLessThanOrEqual(NESTED_JSON.length)

    // The tiling: token spans plus the gaps between them reconstruct the
    // text byte for byte.
    let rebuilt = ""
    let offset = 0
    for (const range of ranges) {
      rebuilt += NESTED_JSON.slice(offset, range.start) + NESTED_JSON.slice(range.start, range.end)
      offset = range.end
    }
    rebuilt += NESTED_JSON.slice(offset)
    expect(rebuilt).toBe(NESTED_JSON)
  })

  it("matches the markup backend token for token — same spans, same classes", () => {
    const ranges = tokenizeToRanges(NESTED_JSON, "json")
    const tokens = markupPieces(NESTED_JSON).filter((piece) => piece.type !== null)
    expect(ranges.map((r) => ({ text: NESTED_JSON.slice(r.start, r.end), type: r.type }))).toEqual(
      tokens,
    )
  })

  it("carries aliases the way markup classes do (null → 'null keyword')", () => {
    const ranges = tokenizeToRanges('{"a": null}', "json")
    expect(ranges.find((r) => r.type.includes("null"))?.type).toBe("null keyword")
  })

  it("yields no spans for a language with no grammar, like the markup fallback", () => {
    expect(tokenizeToRanges("SELECT 1", "fortran")).toEqual([])
  })

  it("round-trips through the packed wire form losslessly", () => {
    const ranges = tokenizeToRanges(NESTED_JSON, "json")
    const packed = packTokenRanges(ranges)
    expect(packed.packed).toBeInstanceOf(Uint32Array)
    expect(packed.packed.length).toBe(ranges.length * 3)
    // The type table is deduplicated — a handful of classes, not one per token.
    expect(packed.types.length).toBeLessThan(10)
    expect(unpackTokenRanges(packed)).toEqual(ranges)
  })
})

describe("grammarTokenClasses", () => {
  it("enumerates the JSON grammar's rule names and aliases", () => {
    const classes = grammarTokenClasses("json")
    for (const expected of [
      "property",
      "string",
      "comment",
      "number",
      "punctuation",
      "operator",
      "boolean",
      "null",
      "keyword", // null's alias
    ]) {
      expect(classes.has(expected), `missing ${expected}`).toBe(true)
    }
  })

  it("returns nothing for an unregistered language", () => {
    expect(grammarTokenClasses("fortran").size).toBe(0)
  })
})
