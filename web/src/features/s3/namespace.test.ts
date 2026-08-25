import { describe, it, expect } from "vitest"
import { isAccountRegionalBucketName, accountRegionalBucketName } from "./namespace"

describe("isAccountRegionalBucketName", () => {
  it("recognises the full -<accountId>-<region>-an shape", () => {
    expect(isAccountRegionalBucketName("logs-000000000000-us-east-1-an")).toBe(true)
  })

  it("recognises other region shapes, including GovCloud", () => {
    expect(isAccountRegionalBucketName("assets-123456789012-ap-southeast-2-an")).toBe(true)
    expect(isAccountRegionalBucketName("assets-123456789012-us-gov-west-1-an")).toBe(true)
  })

  it("does not match a name that merely ends in -an", () => {
    // No account id / region ahead of the suffix — a bucket that happens to be
    // named this way is not an account regional bucket.
    expect(isAccountRegionalBucketName("my-plan-an")).toBe(false)
  })

  it("does not match a global-namespace bucket with no suffix at all", () => {
    expect(isAccountRegionalBucketName("uploads")).toBe(false)
  })

  it("does not match when the account id is not exactly 12 digits", () => {
    expect(isAccountRegionalBucketName("logs-12345-us-east-1-an")).toBe(false)
  })

  it("does not match when the region segment is missing", () => {
    expect(isAccountRegionalBucketName("logs-000000000000-an")).toBe(false)
  })
})

describe("accountRegionalBucketName", () => {
  it("joins prefix, account id, region and the reserved -an suffix", () => {
    expect(accountRegionalBucketName("logs", "000000000000", "us-east-1")).toBe(
      "logs-000000000000-us-east-1-an",
    )
  })

  it("round-trips through isAccountRegionalBucketName", () => {
    const built = accountRegionalBucketName("reports", "111122223333", "eu-west-1")
    expect(isAccountRegionalBucketName(built)).toBe(true)
  })
})
