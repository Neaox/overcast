import { describe, expect, it } from "vitest"
import type { LifecycleRule } from "@aws-sdk/client-s3"

import { toLifecycleRule } from "./s3"

describe("toLifecycleRule", () => {
  it("carries a rule whose only action is on the version history", () => {
    const rule: LifecycleRule = {
      ID: "retire-history",
      Status: "Enabled",
      Filter: { Prefix: "logs/" },
      NoncurrentVersionExpiration: { NoncurrentDays: 30, NewerNoncurrentVersions: 3 },
    }

    expect(toLifecycleRule(rule, 0).noncurrentVersionExpiration).toEqual({
      noncurrentDays: 30,
      newerNoncurrentVersions: 3,
    })
  })

  it("carries noncurrent version transitions", () => {
    const rule: LifecycleRule = {
      ID: "cool-history",
      Status: "Enabled",
      NoncurrentVersionTransitions: [{ NoncurrentDays: 10, StorageClass: "GLACIER" }],
    }

    expect(toLifecycleRule(rule, 0).noncurrentVersionTransitions).toEqual([
      { noncurrentDays: 10, newerNoncurrentVersions: undefined, storageClass: "GLACIER" },
    ])
  })

  it("carries ExpiredObjectDeleteMarker, which is an Expiration with no age", () => {
    const rule: LifecycleRule = {
      ID: "tidy-markers",
      Status: "Enabled",
      Expiration: { ExpiredObjectDeleteMarker: true },
    }

    const mapped = toLifecycleRule(rule, 0)
    expect(mapped.expiredObjectDeleteMarker).toBe(true)
    expect(mapped.expirationDays).toBeUndefined()
  })

  it("drops a noncurrent action that arrives without its NoncurrentDays", () => {
    // NoncurrentDays is the only field the action cannot do without, and the
    // SDK types it as optional. "After undefined days" is worse than silence.
    const rule = {
      ID: "malformed",
      Status: "Enabled",
      NoncurrentVersionExpiration: { NewerNoncurrentVersions: 2 },
      NoncurrentVersionTransitions: [{ StorageClass: "GLACIER" }],
    } as LifecycleRule

    const mapped = toLifecycleRule(rule, 0)
    expect(mapped.noncurrentVersionExpiration).toBeUndefined()
    expect(mapped.noncurrentVersionTransitions).toEqual([])
  })

  it("names an unnamed rule by its position, as the rule list keys on the id", () => {
    expect(toLifecycleRule({ Status: "Enabled" }, 2).id).toBe("rule-3")
  })
})
