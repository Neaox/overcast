import { describe, expect, it } from "vitest"
import { withResumePoint } from "./event-stream.protocol"

describe("withResumePoint", () => {
  const base = "/api/events?ep=http%3A%2F%2Flocalhost%3A4566&region=us-east-1"

  it("leaves the URL alone when there is nothing to resume from", () => {
    expect(withResumePoint(base, null)).toBe(base)
    expect(withResumePoint(base, "")).toBe(base)
  })

  it("adds the resume point without disturbing the existing query", () => {
    const out = new URL(withResumePoint(base, "run7-42"), "https://example.test")
    expect(out.searchParams.get("last_event_id")).toBe("run7-42")
    expect(out.searchParams.get("ep")).toBe("http://localhost:4566")
    expect(out.searchParams.get("region")).toBe("us-east-1")
    expect(out.pathname).toBe("/api/events")
  })

  it("replaces a stale resume point rather than appending a second one", () => {
    const once = withResumePoint(base, "run7-1")
    const twice = withResumePoint(once, "run7-2")
    const out = new URL(twice, "https://example.test")
    expect(out.searchParams.getAll("last_event_id")).toEqual(["run7-2"])
  })

  it("returns a same-origin relative URL, as EventSource is given", () => {
    expect(withResumePoint(base, "run7-42").startsWith("/api/events?")).toBe(true)
  })
})
