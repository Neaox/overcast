import { describe, it, expect } from "vitest"
import {
  estimateExpiry,
  nextUtcMidnight,
  formatExpiryDistance,
  describeLifecycleFilter,
  describeLifecycleActions,
  rulePrefix,
  withNoncurrentPositions,
} from "./lifecycle"
import type { S3LifecycleRule } from "@/types"

function rule(partial: Partial<S3LifecycleRule>): S3LifecycleRule {
  return {
    id: "r",
    status: "Enabled",
    filter: {},
    transitions: [],
    noncurrentVersionTransitions: [],
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

  it("leaves a time already at midnight alone", () => {
    // Rounding it forward would hand the object an extra day, and would put
    // the UI a day out from the emulator's own sweeper.
    expect(nextUtcMidnight(new Date("2026-03-01T00:00:00Z")).toISOString()).toBe(
      "2026-03-01T00:00:00.000Z",
    )
  })

  it("rounds a time one millisecond past midnight to the next day", () => {
    expect(nextUtcMidnight(new Date("2026-03-01T00:00:00.001Z")).toISOString()).toBe(
      "2026-03-02T00:00:00.000Z",
    )
  })
})

describe("estimateExpiry", () => {
  it("reports none when no rule expires the object", () => {
    expect(
      estimateExpiry([rule({ transitions: [{ days: 1, storageClass: "GLACIER" }] })], object),
    ).toEqual({ kind: "none" })
  })

  it("applies the same next-midnight rounding as the emulator", () => {
    const got = estimateExpiry([rule({ id: "d1", expirationDays: 1 })], object)
    expect(got.kind).toBe("expires")
    if (got.kind !== "expires") return
    expect(got.date.toISOString()).toBe("2026-03-03T00:00:00.000Z")
    expect(got.ruleId).toBe("d1")
  })

  it("ignores disabled rules", () => {
    expect(estimateExpiry([rule({ status: "Disabled", expirationDays: 1 })], object)).toEqual({
      kind: "none",
    })
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
      estimateExpiry([rule({ filter: { objectSizeGreaterThan: 100 }, expirationDays: 1 })], object),
    ).toEqual({ kind: "none" })
    expect(
      estimateExpiry([rule({ filter: { objectSizeGreaterThan: 99 }, expirationDays: 1 })], object)
        .kind,
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

  it("does not add an extra day to an object written at midnight", () => {
    const got = estimateExpiry([rule({ expirationDays: 1 })], {
      key: "logs/app.log",
      size: 100,
      lastModified: "2026-03-01T00:00:00Z",
    })
    expect(got.kind === "expires" && got.date.toISOString()).toBe("2026-03-02T00:00:00.000Z")
  })

  it("uses an absolute expiration date as given", () => {
    const got = estimateExpiry([rule({ expirationDate: "2026-06-01T00:00:00.000Z" })], object)
    expect(got.kind === "expires" && got.date.toISOString()).toBe("2026-06-01T00:00:00.000Z")
  })
})

describe("estimateExpiry > noncurrent versions", () => {
  /** The version itself is old; it only stopped being current on 1 March. */
  const version = {
    key: "logs/app.log",
    size: 100,
    lastModified: "2026-01-01T00:00:00Z",
    noncurrent: { since: "2026-03-01T15:04:05Z", rank: 0 },
  }

  it("counts the days from when the version stopped being current", () => {
    const got = estimateExpiry(
      [rule({ noncurrentVersionExpiration: { noncurrentDays: 1 } })],
      version,
    )
    expect(got.kind === "expires" && got.date.toISOString()).toBe("2026-03-03T00:00:00.000Z")
  })

  it("is not expired by the rule's current-version Expiration action", () => {
    // That action adds a delete marker to the current version; it never
    // touches the history underneath it.
    expect(estimateExpiry([rule({ expirationDays: 1 })], version)).toEqual({ kind: "none" })
  })

  it("keeps the newest versions a rule asks it to retain", () => {
    const retainThree = rule({
      noncurrentVersionExpiration: { noncurrentDays: 1, newerNoncurrentVersions: 3 },
    })
    expect(
      estimateExpiry([retainThree], { ...version, noncurrent: { ...version.noncurrent, rank: 2 } }),
    ).toEqual({
      kind: "none",
    })
    expect(
      estimateExpiry([retainThree], { ...version, noncurrent: { ...version.noncurrent, rank: 3 } })
        .kind,
    ).toBe("expires")
  })

  it("does not report a retained version as an unknowable tag rule", () => {
    const got = estimateExpiry(
      [
        rule({
          filter: { tag: { key: "temp", value: "yes" } },
          noncurrentVersionExpiration: { noncurrentDays: 1, newerNoncurrentVersions: 3 },
        }),
      ],
      version,
    )
    expect(got).toEqual({ kind: "none" })
  })

  it("leaves a current version alone when only noncurrent actions are set", () => {
    expect(
      estimateExpiry([rule({ noncurrentVersionExpiration: { noncurrentDays: 1 } })], object),
    ).toEqual({ kind: "none" })
  })
})

describe("withNoncurrentPositions", () => {
  const listing = [
    { key: "a.txt", lastModified: "2026-03-03T00:00:00Z" },
    { key: "a.txt", lastModified: "2026-03-02T00:00:00Z" },
    { key: "a.txt", lastModified: "2026-03-01T00:00:00Z" },
    { key: "b.txt", lastModified: "2026-02-01T00:00:00Z" },
  ]

  it("leaves the current version of each key without a position", () => {
    const positions = withNoncurrentPositions(listing).map((v) => v.noncurrent)
    expect(positions[0]).toBeUndefined()
    expect(positions[3]).toBeUndefined()
  })

  it("dates each noncurrent version from its successor rather than itself", () => {
    const positions = withNoncurrentPositions(listing)
    expect(positions[1].noncurrent?.since).toBe("2026-03-03T00:00:00Z")
    expect(positions[2].noncurrent?.since).toBe("2026-03-02T00:00:00Z")
  })

  it("ranks the noncurrent versions of a key from the newest", () => {
    const positions = withNoncurrentPositions(listing)
    expect(positions[1].noncurrent?.rank).toBe(0)
    expect(positions[2].noncurrent?.rank).toBe(1)
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
            and: {
              prefix: "data/",
              tags: [{ key: "temp", value: "yes" }],
              objectSizeGreaterThan: 10,
            },
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

  it("describes a rule whose only action is on the version history", () => {
    // A versioned bucket's whole retention policy can be this one action, and
    // a rule showing no actions at all reads as a rule that does nothing.
    expect(
      describeLifecycleActions(rule({ noncurrentVersionExpiration: { noncurrentDays: 30 } })),
    ).toEqual(["Expire noncurrent versions after 30 days"])
  })

  it("says how many noncurrent versions are retained regardless of age", () => {
    expect(
      describeLifecycleActions(
        rule({ noncurrentVersionExpiration: { noncurrentDays: 30, newerNoncurrentVersions: 3 } }),
      ),
    ).toEqual(["Expire noncurrent versions after 30 days, keeping the 3 newest"])
  })

  it("describes a noncurrent version transition", () => {
    expect(
      describeLifecycleActions(
        rule({
          noncurrentVersionTransitions: [{ noncurrentDays: 10, storageClass: "GLACIER" }],
        }),
      ),
    ).toEqual(["Mark noncurrent versions GLACIER after 10 days"])
  })

  it("describes the delete-marker cleanup action, which carries no age", () => {
    expect(describeLifecycleActions(rule({ expiredObjectDeleteMarker: true }))).toEqual([
      "Remove expired delete markers",
    ])
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
