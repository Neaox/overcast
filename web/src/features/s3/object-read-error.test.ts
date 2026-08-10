import { describe, expect, it } from "vitest"
import { classifyObjectReadError, describeObjectReadError } from "./object-read-error"

/** What an AWS SDK rejection looks like when only the status survived — a HEAD. */
const sdkError = (status: number) =>
  Object.assign(new Error("UnknownError"), { $metadata: { httpStatusCode: status } })

/** What a named AWS error looks like — a GET, whose XML body carried the code. */
const codedError = (name: string, status: number) =>
  Object.assign(new Error("The specified version does not exist."), {
    name,
    $metadata: { httpStatusCode: status },
  })

describe("classifyObjectReadError", () => {
  it("reads 405 as a delete marker, the only thing S3 refuses that way", () => {
    expect(classifyObjectReadError(sdkError(405), "v2")).toBe("delete-marker")
  })

  it("reads a 404 on a version-addressed read as a version that is gone", () => {
    expect(classifyObjectReadError(sdkError(404), "v2")).toBe("no-such-version")
  })

  it("reads a 404 on a key-addressed read as an object that is gone", () => {
    expect(classifyObjectReadError(sdkError(404), undefined)).toBe("not-found")
  })

  it('treats "null" as a version id, so its 404 is a missing version', () => {
    expect(classifyObjectReadError(sdkError(404), "null")).toBe("no-such-version")
  })

  it("prefers the AWS error code when the response body carried one", () => {
    expect(classifyObjectReadError(codedError("NoSuchVersion", 404))).toBe("no-such-version")
    expect(classifyObjectReadError(codedError("MethodNotAllowed", 405))).toBe("delete-marker")
  })

  it("reads the status a fetch wrapper attaches, not only the SDK's", () => {
    expect(classifyObjectReadError(Object.assign(new Error("HTTP 405"), { status: 405 }))).toBe(
      "delete-marker",
    )
  })

  it.each([500, 403, undefined])("leaves %s unclassified", (status) => {
    const error = status === undefined ? new Error("network down") : sdkError(status)
    expect(classifyObjectReadError(error, "v2")).toBe("other")
  })

  it("does not throw on a rejection that is not an object", () => {
    expect(classifyObjectReadError("boom")).toBe("other")
  })
})

describe("describeObjectReadError", () => {
  it("explains a delete marker as a tombstone rather than a failure", () => {
    expect(describeObjectReadError(sdkError(405), "v2")).toMatch(/delete marker/i)
  })

  it("names the version that no longer exists", () => {
    expect(describeObjectReadError(sdkError(404), "v2")).toContain("Version v2")
  })

  it("does not invent a version for a key-addressed read", () => {
    expect(describeObjectReadError(sdkError(404))).not.toMatch(/version/i)
  })

  it("falls back to the underlying message when the cause is not a version one", () => {
    expect(describeObjectReadError(sdkError(500), "v2")).toBe("UnknownError")
  })

  it("still says something when the rejection carries no message", () => {
    expect(describeObjectReadError({})).toBe("This object could not be read.")
  })
})
