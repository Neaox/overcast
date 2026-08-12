import { describe, expect, it } from "vitest"
import { describeOmission } from "./omission"

describe("describeOmission", () => {
  it("says nothing when no body was lost", () => {
    // A request that simply had no body must not be described as one we
    // dropped — that distinction is the whole reason the field exists.
    expect(describeOmission(undefined, false)).toBeNull()
    expect(describeOmission("", false)).toBeNull()
  })

  it("reports a size truncation as partial, because a prefix is shown", () => {
    const got = describeOmission("size", true)
    expect(got?.partial).toBe(true)
    expect(got?.label).toMatch(/truncated/i)
  })

  it("names the per-trace budget, and says the rest of the hop survived", () => {
    // The deploy case: the reader needs to know the body is gone but the
    // timing, status and ordering they are looking at are still real.
    const got = describeOmission("trace-budget", false)
    expect(got?.partial).toBe(false)
    expect(got?.label).toMatch(/not captured/i)
    expect(got?.detail).toMatch(/8 MiB/)
    expect(got?.seeOwnTrace).toBeFalsy()
  })

  it("points at the hop's own trace when the body is still readable there", () => {
    // A hop is dispatched through the router, so it is a request in its own
    // right and the parent's copy of its body is a duplicate. Dropping that
    // copy loses nothing while the callee's trace is retained — saying only
    // "not captured" sends the reader looking for data one click away.
    const got = describeOmission("trace-budget", false, true)
    expect(got?.seeOwnTrace).toBe(true)
    expect(got?.detail).toMatch(/own request/i)
    expect(got?.detail).toMatch(/in full/i)
  })

  it("distinguishes a streamed body from one that was dropped to save space", () => {
    const got = describeOmission("streaming", false)
    expect(got?.partial).toBe(false)
    expect(got?.detail).toMatch(/stream/i)
    expect(got?.detail).not.toMatch(/budget/i)
  })

  it("still describes an unknown reason rather than rendering nothing", () => {
    // A reason added server-side before the UI knows about it must not read
    // as "there was no body".
    const got = describeOmission("something-new", false)
    expect(got).not.toBeNull()
    expect(got?.label).toMatch(/not captured/i)
  })
})
