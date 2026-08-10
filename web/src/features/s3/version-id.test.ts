import { describe, it, expect } from "vitest"
import { shortVersionId } from "./version-id"

describe("shortVersionId", () => {
  // Three versions of one key, minted a few milliseconds apart by the token
  // scheme in internal/services/s3/version.go. The first eight characters are
  // the top of the inverted timestamp and are identical across all three,
  // which is exactly what a leading-edge truncation would show.
  const versions = [
    "SSQP08INVMSJVVVVVVS9OEJH",
    "SSQP08INPVP7VVVVVVRIV06K",
    "SSQP08INK8LRVVVVVVRE26O6",
  ]

  it("distinguishes versions of one key that share a leading run", () => {
    expect(versions.map((v) => v.slice(0, 8))).toEqual(["SSQP08IN", "SSQP08IN", "SSQP08IN"])
    expect(new Set(versions.map(shortVersionId)).size).toBe(3)
  })

  it("shows the end of the id, which is the counter and salt", () => {
    expect(shortVersionId(versions[0])).toBe("…VVS9OEJH")
  })

  it("leaves the null version's id alone", () => {
    expect(shortVersionId("null")).toBe("null")
  })

  it("does not truncate an id that would not get shorter for it", () => {
    expect(shortVersionId("123456789")).toBe("123456789")
    expect(shortVersionId("1234567890")).toBe("…34567890")
  })
})
