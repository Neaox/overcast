import { highlightCode } from "./highlight-code"

describe("highlightCode", () => {
  it("marks up the grammar of the named language", () => {
    expect(highlightCode('{"a": 1}', "json")).toContain("token property")
    expect(highlightCode("body { color: red }", "css")).toContain("token selector")
    expect(highlightCode("const x = 1", "javascript")).toContain("token keyword")
    expect(highlightCode("<root><a/></root>", "markup")).toContain("token tag")
  })

  it("escapes the text it highlights", () => {
    expect(highlightCode('{"a": "<img src=x>"}', "json")).not.toContain("<img")
  })

  it("falls back to escaped plain text for a grammar that is not loaded", () => {
    // Callers derive the language from content types and file extensions; an
    // exotic one must degrade to text, not throw inside a row render.
    expect(highlightCode("SELECT <b> FROM t", "sql")).toBe("SELECT &lt;b&gt; FROM t")
  })

  it("returns the identical string for a repeated (text, language) pair", () => {
    const text = '{"requestId":"13aa488f","status":"timeout"}'

    // Identity, not just equality: React skips the DOM write when the value
    // handed to `dangerouslySetInnerHTML` is unchanged, and a row that
    // mutates nothing costs nothing downstream — no style recalc, and no
    // mutation record for whatever extensions are observing the document.
    expect(highlightCode(text, "json")).toBe(highlightCode(text, "json"))
  })

  it("keys the cache by language, so one text can wear two grammars", () => {
    const text = "a { b: 1 }"
    const asCss = highlightCode(text, "css")
    const asJs = highlightCode(text, "javascript")
    expect(asCss).not.toBe(asJs)
    // And each language still hits its own cached entry.
    expect(highlightCode(text, "css")).toBe(asCss)
    expect(highlightCode(text, "javascript")).toBe(asJs)
  })

  it("still highlights a document too large to be worth holding onto", () => {
    const huge = JSON.stringify({ blob: "x".repeat(120_000) })
    expect(highlightCode(huge, "json")).toContain("token")
  })
})
