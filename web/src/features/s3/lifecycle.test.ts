import { describe, it, expect } from "vitest"
import {
  estimateExpiry,
  nextUtcMidnight,
  formatExpiryDistance,
  describeLifecycleFilter,
  describeLifecycleActions,
  rulePrefix,
} from "./lifecycle"
import type { S3LifecycleRule } from "@/types"

function rule(partial: Partial<S3LifecycleRule>): S3LifecycleRule {
  return {
    id: "r",
    status: "Enabled",
    filter: {},
    transitions: [],
    ...partial,
  }
}

const object = { key: "logs/app.log", size: 100, lastModified: "2026-03-01T15:04:05Z" }

describe("nextUtcMidnight", () => {
  it("rounds up to the following UTC midnight", () => {
    expect(nextUtcMidnight(new Date("2026-03-01T15:04:05Z")).toISOString()).toBe(
      "2026-03-02T00:00:00.000Z",
    )
  })

  it("moves a time already at midnight to the next day", () => {
    expect(nextUtcMidnight(new Date("2026-03-01T00:00:00Z")).toISOString()).toBe(
      "2026-03-02T00:00:00.000Z",
    )
  })
})

describe("estimateExpiry", () => {
  it("reports none when no rule expires the object", () => {
    expect(estimateExpiry([rule({ transitions: [{ days: 1, storageClass: "GLACIER" }] })], object))
      .toEqual({ kind: "none" })
  })

  it("applies the same next-midnight rounding as the emulator", () => {
    const got = estimateExpiry([rule({ id: "d1", expirationDays: 1 })], object)
    expect(got.kind).toBe("expires")
    if (got.kind !== "expires") return
    expect(got.date.toISOString()).toBe("2026-03-03T00:00:00.000Z")
    expect(got.ruleId).toBe("d1")
  })

  it("ignores disabled rules", () => {
    expect(
      estimateExpiry([rule({ status: "Disabled", expirationDays: 1 })], object),
    ).toEqual({ kind: "none" })
  })

  it("ignores rules whose prefix does not select the object", () => {
    expect(
      estimateExpiry([rule({ filter: { prefix: "tmp/" }, expirationDays: 1 })], object),
    ).toEqual({ kind: "none" })
  })

  it("honours the deprecated rule-level prefix form", () => {
    const got = estimateExpiry(
      [rule({ prefix: "logs/", filter: undefined, expirationDays: 2 })],
      object,
    )
    expect(got.kind).toBe("expires")
  })

  it("picks the earliest of several matching rules", () => {
    const got = estimateExpiry(
      [rule({ id: "slow", expirationDays: 30 }), rule({ id: "fast", expirationDays: 1 })],
      object,
    )
    expect(got.kind === "expires" && got.ruleId).toBe("fast")
  })

  it("applies exclusive object-size bounds", () => {
    expect(
      estimateExpiry(
        [rule({ filter: { objectSizeGreaterThan: 100 }, expirationDays: 1 })],
        object,
      ),
    ).toEqual({ kind: "none" })
    expect(
      estimateExpiry(
        [rule({ filter: { objectSizeGreaterThan: 99 }, expirationDays: 1 })],
        object,
      ).kind,
    ).toBe("expires")
  })

  it("reports unknown rather than guessing when a rule filters on tags", () => {
    expect(
      estimateExpiry(
        [rule({ id: "tagged", filter: { tag: { key: "temp", value: "yes" } }, expirationDays: 1 })],
        object,
      ),
    ).toEqual({ kind: "unknown", ruleId: "tagged" })
  })

  it("prefers a known expiry over an unknowable one", () => {
    const got = estimateExpiry(
      [
        rule({ id: "tagged", filter: { tag: { key: "temp", value: "yes" } }, expirationDays: 1 }),
        rule({ id: "plain", expirationDays: 5 }),
      ],
      object,
    )
    expect(got.kind === "expires" && got.ruleId).toBe("plain")
  })

  it("uses an absolute expiration date as given", () => {
    const got = estimateExpiry([rule({ expirationDate: "2026-06-01T00:00:00.000Z" })], object)
    expect(got.kind === "expires" && got.date.toISOString()).toBe("2026-06-01T00:00:00.000Z")
  })
})

describe("formatExpiryDistance", () => {
  const now = new Date("2026-03-01T00:00:00Z")

  it("counts whole days", () => {
    expect(formatExpiryDistance(new Date("2026-03-04T00:00:00Z"), now)).toBe("in 3d")
  })

  it("falls back to hours inside a day", () => {
    expect(formatExpiryDistance(new Date("2026-03-01T05:00:00Z"), now)).toBe("in 5h")
  })

  it("reports a past expiry as due", () => {
    expect(formatExpiryDistance(new Date("2026-02-01T00:00:00Z"), now)).toBe("due")
  })
})

describe("describeLifecycleFilter", () => {
  it("names the whole bucket for an empty filter", () => {
    expect(describeLifecycleFilter(rule({}))).toBe("whole bucket")
  })

  it("lists every predicate of an And filter", () => {
    expect(
      describeLifecycleFilter(
        rule({
          filter: {
            and: { prefix: "data/", tags: [{ key: "temp", value: "yes" }], objectSizeGreaterThan: 10 },
          },
        }),
      ),
    ).toBe("prefix data/ · tag temp=yes · size > 10 B")
  })
})

describe("describeLifecycleActions", () => {
  it("describes every action a rule takes", () => {
    expect(
      describeLifecycleActions(
        rule({
          expirationDays: 30,
          transitions: [{ days: 1, storageClass: "GLACIER" }],
          abortIncompleteMultipartUploadDays: 7,
        }),
      ),
    ).toEqual([
      "Expire after 30 days",
      "Mark GLACIER after 1 day",
      "Abort incomplete uploads after 7 days",
    ])
  })

  it("returns nothing for a rule with no actions", () => {
    expect(describeLifecycleActions(rule({}))).toEqual([])
  })
})

describe("rulePrefix", () => {
  it("reads the prefix out of either form", () => {
    expect(rulePrefix(rule({ prefix: "legacy/", filter: undefined }))).toBe("legacy/")
    expect(rulePrefix(rule({ filter: { prefix: "modern/" } }))).toBe("modern/")
    expect(rulePrefix(rule({ filter: { and: { prefix: "and/", tags: [] } } }))).toBe("and/")
    expect(rulePrefix(rule({}))).toBe("")
  })
})
