import { describe, expect, it } from "vitest"
import { formatPreciseTimeOfDay } from "./format"

describe("formatPreciseTimeOfDay", () => {
  it("includes milliseconds so closely spaced scheduler events remain distinguishable", () => {
    // Given: an event timestamp with sub-second precision.
    const timestamp = "2026-08-05T07:06:31.482Z"

    // When: the compact event time is formatted.
    const formatted = formatPreciseTimeOfDay(timestamp)

    // Then: the viewer can distinguish multiple events emitted within the same second.
    expect(formatted).toMatch(/:\d{2}\.482/)
  })
})
