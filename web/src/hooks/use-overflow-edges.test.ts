import { describe, expect, it } from "vitest"
import { overflowEdges } from "./use-overflow-edges"

/**
 * The measurement behind the scroll affordance. jsdom does no layout, so the
 * pure function is the testable half — and it is the half with the judgement
 * calls in it: which edge counts as "more this way", and how much rounding to
 * forgive before drawing a shadow nobody can scroll towards.
 */
describe("overflowEdges", () => {
  it("reports neither edge when the content fits", () => {
    expect(overflowEdges({ scrollLeft: 0, scrollWidth: 800, clientWidth: 800 })).toEqual({
      start: false,
      end: false,
    })
  })

  it("reports the end edge when columns run past the right of the pane", () => {
    expect(overflowEdges({ scrollLeft: 0, scrollWidth: 1200, clientWidth: 400 })).toEqual({
      start: false,
      end: true,
    })
  })

  it("reports both edges mid-scroll", () => {
    expect(overflowEdges({ scrollLeft: 400, scrollWidth: 1200, clientWidth: 400 })).toEqual({
      start: true,
      end: true,
    })
  })

  it("drops the end edge once the scroller is against its right limit", () => {
    expect(overflowEdges({ scrollLeft: 800, scrollWidth: 1200, clientWidth: 400 })).toEqual({
      start: true,
      end: false,
    })
  })

  it("forgives a sub-pixel overflow, so a table that fits draws no shadow", () => {
    // scrollWidth and clientWidth are integers rounded from fractional layout,
    // and a permanent one-pixel shadow on a table nobody can scroll is worse
    // than no shadow at all.
    expect(overflowEdges({ scrollLeft: 0, scrollWidth: 801, clientWidth: 800 })).toEqual({
      start: false,
      end: false,
    })
  })

  it("measures a right-to-left scroller, whose scrollLeft runs negative", () => {
    expect(overflowEdges({ scrollLeft: -400, scrollWidth: 1200, clientWidth: 400 })).toEqual({
      start: true,
      end: true,
    })
  })
})
