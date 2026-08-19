/**
 * The worker's protocol, exercised through its exported handler — jsdom has
 * no real Worker, and the wire loop is one line around this function.
 */
import { handleHighlightRequest } from "./highlight-worker"
import { tokenizeToRanges, unpackTokenRanges } from "./prism-ranges"

describe("handleHighlightRequest", () => {
  it("answers a request with the same id and the packed tokenization", () => {
    const text = '{"level": "warn", "attempt": 2, "ok": false}'
    const response = handleHighlightRequest({ id: 7, text, language: "json" })

    expect(response.id).toBe(7)
    expect(response.packed).toBeInstanceOf(Uint32Array)
    expect(unpackTokenRanges(response)).toEqual(tokenizeToRanges(text, "json"))
  })

  it("answers an unknown language with an empty tokenization, not an error", () => {
    const response = handleHighlightRequest({ id: 1, text: "SELECT 1", language: "sql" })
    expect(response.packed.length).toBe(0)
    expect(unpackTokenRanges(response)).toEqual([])
  })
})
