/**
 * The proximity half of row hydration: `nearViewport` bounds each hydration
 * commit to about two viewports' worth of rows, so a wide overscan window of
 * pretty-printed documents cannot hydrate in one giant commit (the freeze
 * that motivated it — see the module docs).
 */
import { describe, expect, it } from "vitest"
import { nearViewport } from "./use-scroll-settled"

const instance = (scrollOffset: number | null, height: number | null) => ({
  scrollOffset,
  scrollRect: height == null ? null : { width: 0, height },
})

describe("nearViewport", () => {
  it("includes the viewport and one viewport beyond each edge zone", () => {
    const inst = instance(1000, 500)
    // On screen.
    expect(nearViewport({ start: 1100, end: 1300 }, inst)).toBe(true)
    // Below, within the two-viewport lookahead (start < offset + 2*viewport).
    expect(nearViewport({ start: 1900, end: 2100 }, inst)).toBe(true)
    // Above, within one viewport behind (end > offset - viewport).
    expect(nearViewport({ start: 400, end: 600 }, inst)).toBe(true)
  })

  it("excludes far overscan rows on both sides", () => {
    const inst = instance(1000, 500)
    expect(nearViewport({ start: 2100, end: 2300 }, inst)).toBe(false)
    expect(nearViewport({ start: 200, end: 450 }, inst)).toBe(false)
  })

  it("tolerates a virtualizer that has not measured yet", () => {
    // Null offset reads as 0; null rect falls back to a nominal viewport —
    // first paint must hydrate the first screen, not defer it.
    expect(nearViewport({ start: 0, end: 40 }, instance(null, null))).toBe(true)
  })
})
