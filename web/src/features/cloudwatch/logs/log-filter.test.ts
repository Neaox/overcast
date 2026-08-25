/**
 * `resolveLogFilter` is the one function every `LogFilter` reader turns into a
 * FilterLogEvents request — see the module docs for why a pile of
 * hand-assembled `{startTime, endTime, ...}` objects was the thing this
 * replaced. These tests pin the two `time` variants' defining behaviour: a
 * relative window slides with `now`, an absolute one ignores it outright.
 */
import { describe, expect, it } from "vitest"
import { normalizeLogFilter, resolveLogFilter, type LogFilter } from "./log-filter"

describe("resolveLogFilter", () => {
  it("resolves a relative window against `now`, open-ended on the end", () => {
    const filter: LogFilter = { group: "/aws/lambda/fn", time: { kind: "relative", token: "1h" } }
    const resolved = resolveLogFilter(filter, 10_000_000)
    expect(resolved.groupName).toBe("/aws/lambda/fn")
    expect(resolved.opts.startTime).toBe(10_000_000 - 60 * 60 * 1000)
    expect(resolved.opts.endTime).toBeUndefined()
  })

  it("slides a relative window forward as `now` advances — the point of auto-refresh", () => {
    const filter: LogFilter = { group: "g", time: { kind: "relative", token: "15m" } }
    const first = resolveLogFilter(filter, 1_000_000)
    const second = resolveLogFilter(filter, 2_000_000)
    expect(second.opts.startTime).toBeGreaterThan(first.opts.startTime!)
    expect(second.opts.startTime! - first.opts.startTime!).toBe(1_000_000)
  })

  it("pins an absolute window regardless of `now`", () => {
    const filter: LogFilter = {
      group: "g",
      time: { kind: "absolute", startMs: 500, endMs: 1500 },
    }
    const atOneNow = resolveLogFilter(filter, 10_000)
    const atAnotherNow = resolveLogFilter(filter, 999_999_999)
    expect(atOneNow.opts.startTime).toBe(500)
    expect(atOneNow.opts.endTime).toBe(1500)
    expect(atAnotherNow.opts).toEqual(atOneNow.opts)
  })

  it("prefers `streams` over `stream` and drops an empty streams array", () => {
    const withStream = resolveLogFilter(
      { group: "g", stream: "s1", time: { kind: "relative", token: "1h" } },
      0,
    )
    expect(withStream.opts.logStreamNames).toEqual(["s1"])

    const withStreams = resolveLogFilter(
      { group: "g", stream: "s1", streams: ["s2", "s3"], time: { kind: "relative", token: "1h" } },
      0,
    )
    expect(withStreams.opts.logStreamNames).toEqual(["s2", "s3"])

    const withNeither = resolveLogFilter({ group: "g", time: { kind: "relative", token: "1h" } }, 0)
    expect(withNeither.opts.logStreamNames).toBeUndefined()
  })

  it("carries pattern and limit through untouched", () => {
    const resolved = resolveLogFilter(
      { group: "g", time: { kind: "relative", token: "1h" }, pattern: "ERROR", limit: 50 },
      0,
    )
    expect(resolved.opts.filterPattern).toBe("ERROR")
    expect(resolved.opts.limit).toBe(50)
  })
})

describe("normalizeLogFilter", () => {
  it("treats an absent stream and an empty streams array as equivalent to no stream filter", () => {
    const time: LogFilter["time"] = { kind: "relative", token: "1h" }
    const a = normalizeLogFilter({ group: "g", time })
    const b = normalizeLogFilter({ group: "g", time, streams: [] })
    expect(a).toEqual(b)
    expect(a.streams).toBeUndefined()
  })

  it("treats a blank pattern the same as no pattern, so the query key does not churn on whitespace", () => {
    const time: LogFilter["time"] = { kind: "relative", token: "1h" }
    const blank = normalizeLogFilter({ group: "g", time, pattern: "   " })
    const absent = normalizeLogFilter({ group: "g", time })
    expect(blank).toEqual(absent)
  })

  it("folds `stream` into `streams` so both spellings key identically", () => {
    const time: LogFilter["time"] = { kind: "relative", token: "1h" }
    const viaStream = normalizeLogFilter({ group: "g", time, stream: "s1" })
    const viaStreams = normalizeLogFilter({ group: "g", time, streams: ["s1"] })
    expect(viaStream).toEqual(viaStreams)
  })
})
