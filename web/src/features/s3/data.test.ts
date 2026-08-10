import { describe, expect, it } from "vitest"
import { s3Keys } from "./data"

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
