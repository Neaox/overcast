import { describe, expect, it } from "vitest"
import { describeRetention, formatWindow } from "./retention"
import type { TraceCountResponse } from "@/types"

const base: TraceCountResponse = {
  count: 10,
  capacity: 1200,
  live: 10,
  pinned: 0,
  internal: 0,
  floor: 1000,
  ceiling: 10000,
  window: 3_600_000_000_000,
  pinnedLimit: 1000,
  bytes: 0,
  bytesBudget: 536870912,
  dropped: { capacity: 0, aged: 0, bytes: 0, pinnedCap: 0, internal: 0 },
}

describe("describeRetention", () => {
  it("says nothing on a fresh emulator", () => {
    // "0 traces dropped" is noise, and worse, it implies something happened.
    expect(describeRetention(base)).toBeNull()
    expect(describeRetention(undefined)).toBeNull()
  })

  it("ignores internal polling, which recycles itself constantly", () => {
    // Health checks and the console's own requests churn through their ring
    // continuously; counting them would swamp the number that matters.
    const got = describeRetention({ ...base, dropped: { ...base.dropped, internal: 9999 } })
    expect(got).toBeNull()
  })

  it("names the window when traces aged out, because that is actionable", () => {
    const got = describeRetention({ ...base, dropped: { ...base.dropped, aged: 1204 } })
    expect(got?.headline).toBe("1,204 older traces no longer retained")
    expect(got?.reasons).toEqual(["1,204 aged out after 1h"])
  })

  it("separates the rules, since they are different answers", () => {
    // "we ran out of room" and "you left it too long" lead somewhere different:
    // only one of them is fixed by raising a limit.
    const got = describeRetention({
      ...base,
      dropped: { ...base.dropped, aged: 5, capacity: 3, bytes: 2, pinnedCap: 1 },
    })
    expect(got?.headline).toBe("11 older traces no longer retained")
    expect(got?.reasons).toHaveLength(4)
    expect(got?.reasons[0]).toMatch(/aged out/)
    expect(got?.reasons[1]).toMatch(/pushed out by newer/)
    expect(got?.reasons[2]).toMatch(/memory budget/)
    expect(got?.reasons[3]).toMatch(/failures displaced/)
  })

  it("uses the singular for one trace", () => {
    const got = describeRetention({ ...base, dropped: { ...base.dropped, capacity: 1 } })
    expect(got?.headline).toBe("1 older trace no longer retained")
  })
})

describe("formatWindow", () => {
  it("renders whole hours, minutes and seconds", () => {
    expect(formatWindow(3_600_000_000_000)).toBe("1h")
    expect(formatWindow(2_700_000_000_000)).toBe("45m")
    expect(formatWindow(90_000_000_000)).toBe("90s")
  })
})
