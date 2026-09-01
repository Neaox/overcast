import { afterEach, describe, expect, it, vi } from "vitest"
import type { LifecycleRule } from "@aws-sdk/client-s3"

import { s3, toLifecycleRule } from "./s3"

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

/** The query string of a BFF download URL, which is where the version lives. */
function query(url: string): URLSearchParams {
  return new URL(url, "http://console.test").searchParams
}

describe("s3.getObjectDownloadUrl", () => {
  it("omits versionId when the caller named no version", () => {
    expect(query(s3.getObjectDownloadUrl("b", "report.csv")).has("versionId")).toBe(false)
  })

  it("addresses the named version rather than the key", () => {
    expect(query(s3.getObjectDownloadUrl("b", "report.csv", "v2")).get("versionId")).toBe("v2")
  })

  it('forwards "null", the version id of an object stored while unversioned', () => {
    // The bug this guards against is treating the id as falsy: "null" is a real
    // version id, and dropping it would silently fetch the current version.
    expect(query(s3.getObjectDownloadUrl("b", "report.csv", "null")).get("versionId")).toBe("null")
  })

  it("forwards an empty version id rather than discarding it", () => {
    // An empty string is not a version the emulator can serve, but turning it
    // into "whichever is current" hides the mistake behind a plausible answer.
    expect(query(s3.getObjectDownloadUrl("b", "report.csv", "")).get("versionId")).toBe("")
  })

  it("keeps the endpoint parameters alongside the version", () => {
    const params = query(s3.getObjectDownloadUrl("b", "report.csv", "v2"))
    expect(params.get("x-overcast-endpoint")).toBe("http://localhost:4566")
    expect(params.get("versionId")).toBe("v2")
  })
})

describe("s3.getObjectText", () => {
  afterEach(() => vi.unstubAllGlobals())

  const respond = (body: string, status: number, contentRange?: string) => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(body, {
          status,
          headers: contentRange ? { "Content-Range": contentRange } : {},
        }),
      ),
    )
  }

  it("reports truncation when the range was clamped to a larger object", async () => {
    respond("x".repeat(64), 206, "bytes 0-63/5242880")
    await expect(s3.getObjectText("b", "big.log", undefined, 64)).resolves.toMatchObject({
      truncated: true,
    })
  })

  it("does not report truncation when a 206 served the whole object", async () => {
    // S3 answers 206 for ANY satisfiable range — a 36-byte object read with
    // Range: bytes=0-1048575 comes back 206, "bytes 0-35/36", complete.
    respond("tiny", 206, "bytes 0-3/4")
    await expect(s3.getObjectText("b", "tiny.txt")).resolves.toEqual({
      text: "tiny",
      truncated: false,
    })
  })

  it("does not report truncation on a 200 that ignored the range", async () => {
    respond("whole body", 200)
    await expect(s3.getObjectText("b", "plain.txt")).resolves.toEqual({
      text: "whole body",
      truncated: false,
    })
  })

  it("counts an unreadable Content-Range as complete rather than cut", async () => {
    respond("body", 206)
    await expect(s3.getObjectText("b", "odd.txt")).resolves.toMatchObject({ truncated: false })
  })
})
