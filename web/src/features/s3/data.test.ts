import { afterEach, describe, expect, it, vi } from "vitest"
import { s3Keys, s3ObjectHistoryQueryOptions } from "./data"
import { s3 } from "@/services/api"
import type { S3ObjectVersion } from "@/types"

describe("s3Keys.objectMeta", () => {
  it("gives each version of a key its own cache entry", () => {
    expect(s3Keys.objectMeta("b", "report.csv", "v1")).not.toEqual(
      s3Keys.objectMeta("b", "report.csv", "v2"),
    )
  })

  it('separates the current version from the literal version id "null"', () => {
    // Both are legitimate ways to address an object and they can resolve to
    // different bytes, so sharing one cache entry would show one where the
    // other was asked for.
    expect(s3Keys.objectMeta("b", "report.csv")).not.toEqual(
      s3Keys.objectMeta("b", "report.csv", "null"),
    )
  })

  it("is stable for the same version, so a reopened row is a cache hit", () => {
    expect(s3Keys.objectMeta("b", "report.csv", "v1")).toEqual(
      s3Keys.objectMeta("b", "report.csv", "v1"),
    )
  })
})

describe("s3Keys.objectPreview", () => {
  it("scopes the preview body to the version its metadata describes", () => {
    expect(s3Keys.objectPreview("b", "report.csv", "v1")).not.toEqual(
      s3Keys.objectPreview("b", "report.csv", "v2"),
    )
  })

  it("nests under the metadata key, so invalidating one reaches the other", () => {
    const meta = s3Keys.objectMeta("b", "report.csv", "v1")
    expect(s3Keys.objectPreview("b", "report.csv", "v1").slice(0, meta.length)).toEqual([...meta])
  })
})

describe("s3ObjectHistoryQueryOptions", () => {
  function version(key: string, versionId: string, over: Partial<S3ObjectVersion> = {}) {
    return {
      key,
      versionId,
      isLatest: false,
      isDeleteMarker: false,
      lastModified: "2026-01-01T00:00:00.000Z",
      size: 10,
      etag: "abc",
      storageClass: "STANDARD",
      ...over,
    }
  }

  function run(page: { versions: S3ObjectVersion[]; isTruncated: boolean }, key = "logs/app.log") {
    const listObjectVersions = vi.fn().mockResolvedValue({ ...page, prefixes: [] })
    vi.spyOn(s3, "listObjectVersions").mockImplementation(listObjectVersions)
    const options = s3ObjectHistoryQueryOptions("b", key)
    return { result: options.queryFn!({} as never), listObjectVersions }
  }

  afterEach(() => vi.restoreAllMocks())

  it("drops the neighbours a prefix match drags in", async () => {
    // ListObjectVersions filters by prefix, so "logs/app.log" also answers with
    // "logs/app.log.bak" — whose revisions are not this object's history.
    const { result } = run({
      versions: [
        version("logs/app.log", "v2", { isLatest: true }),
        version("logs/app.log", "v1"),
        version("logs/app.log.bak", "b1", { isLatest: true }),
      ],
      isTruncated: false,
    })

    expect((await result).versions.map((v) => v.versionId)).toEqual(["v2", "v1"])
  })

  it("reads the whole key, not one folder of it", async () => {
    const { result, listObjectVersions } = run({ versions: [], isTruncated: false })
    await result
    expect(listObjectVersions).toHaveBeenCalledWith(
      "b",
      expect.objectContaining({ prefix: "logs/app.log", delimiter: "" }),
    )
  })

  it("does not cry truncation when the page merely ran on into the next key", async () => {
    // Keys come back ascending, so reaching a later key proves this one's
    // revisions were all read — the cap fell somewhere that is not this object.
    const { result } = run({
      versions: [
        version("logs/app.log", "v1", { isLatest: true }),
        version("logs/app.log.bak", "b1", { isLatest: true }),
      ],
      isTruncated: true,
    })

    expect((await result).isTruncated).toBe(false)
  })

  it("says so when the page filled up inside this key's own history", async () => {
    const { result } = run({
      versions: [version("logs/app.log", "v2", { isLatest: true }), version("logs/app.log", "v1")],
      isTruncated: true,
    })

    expect((await result).isTruncated).toBe(true)
  })
})
