import { describe, expect, it } from "vitest"
import { compileFilterHighlighter, parseLogFilterTerms } from "@/features/cloudwatch/logs/tail"

/*
 * The viewers highlight filter matches inside every visible row, and the
 * virtualizer re-renders those rows on every scroll frame. Compiling the
 * pattern per row made its cost scale with rows rendered rather than with edits
 * to the filter box, so the matcher is built once and shared — which only works
 * if one instance is safe to use repeatedly.
 */

describe("compileFilterHighlighter", () => {
  it("selects nothing for an empty pattern", () => {
    expect(compileFilterHighlighter("")).toBeNull()
    expect(compileFilterHighlighter("   ")).toBeNull()
  })

  it("matches any of the pattern's terms, case-insensitively", () => {
    const matcher = compileFilterHighlighter("ERROR timeout")!

    expect("connection timeout".split(matcher)).toEqual(["connection ", "timeout", ""])
    expect("an Error happened".split(matcher)).toEqual(["an ", "Error", " happened"])
  })

  it("keeps a quoted phrase together", () => {
    expect(parseLogFilterTerms('"request failed"')).toEqual(["request failed"])

    const matcher = compileFilterHighlighter('"request failed"')!

    expect("the request failed twice".split(matcher)).toEqual(["the ", "request failed", " twice"])
  })

  it("treats regex metacharacters in a term as literal text", () => {
    const matcher = compileFilterHighlighter("a.b(c)")!

    expect("x a.b(c) y".split(matcher)).toEqual(["x ", "a.b(c)", " y"])
    // The dot must not have matched any character, which an unescaped pattern
    // would have done here.
    expect("axbXcX".split(matcher)).toEqual(["axbXcX"])
  })

  it("gives the same result every time one instance is reused", () => {
    const matcher = compileFilterHighlighter("ERROR")!

    // A global regex carries `lastIndex` between `exec`/`test` calls, which is
    // what would make a shared instance skip every other row. `split` clones
    // internally, so reuse is safe — pinned here because the sharing is the
    // whole point of compiling once.
    const first = "ERROR one".split(matcher)
    const second = "ERROR one".split(matcher)
    const third = "later ERROR".split(matcher)

    expect(second).toEqual(first)
    expect(third).toEqual(["later ", "ERROR", ""])
  })

  it("leaves a message with no match as a single part", () => {
    const matcher = compileFilterHighlighter("ERROR")!

    expect("all is well".split(matcher)).toEqual(["all is well"])
  })
})
