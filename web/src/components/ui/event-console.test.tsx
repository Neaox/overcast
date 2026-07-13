import { describe, expect, it } from "vitest"
import { defaultEventSummary } from "./event-summary"

describe("EventConsole", () => {
  it("summarizes S3 bucket lifecycle events from resource payload names", () => {
    const summary = defaultEventSummary({
      type: "s3:BucketCreated",
      source: "s3",
      time: "2026-07-14T12:00:00Z",
      payload: { name: "assets-bucket" },
    })

    expect(summary).toBe("s3://assets-bucket")
  })

  it("summarizes S3 object events from notification payload bucket and key", () => {
    const summary = defaultEventSummary({
      type: "s3:ObjectCreated:*",
      source: "s3",
      time: "2026-07-14T12:00:00Z",
      payload: { Bucket: "assets-bucket", Key: "images/logo.png", Size: 2048 },
    })

    expect(summary).toBe("s3://assets-bucket/images/logo.png (2.0 KB)")
  })
})
