/**
 * The worker's protocol, exercised through its exported handler — jsdom has
 * no real Worker, and the wire loop is one line around this function.
 */
import { handleHighlightRequest } from "./highlight-worker"
import { tokenizeToRanges, unpackTokenRanges } from "./prism-ranges"

describe("Prism worker-scope config", () => {
  it("disables Prism's own worker message handler before prismjs loads", async () => {
    // Prism core reads this flag at ITS module init: in a scope with no
    // `document` and the flag unset it registers a message listener that
    // JSON.parses every event — which throws on our structured-cloned
    // protocol, fires the Worker's error event, and retires the worker for
    // the session. The barrel must therefore set the flag in an import that
    // executes first; the regression only bites in a real browser (jsdom has
    // a document), so this pins the mechanism it depends on.
    vi.resetModules()
    const globalScope = globalThis as { Prism?: unknown }
    const original = globalScope.Prism
    delete globalScope.Prism
    try {
      const { default: freshPrism } = await import("./prism")
      expect(
        (freshPrism as { disableWorkerMessageHandler?: boolean }).disableWorkerMessageHandler,
      ).toBe(true)
    } finally {
      globalScope.Prism = original
      vi.resetModules()
    }
  })
})

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
