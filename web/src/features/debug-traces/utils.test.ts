import { describe, expect, it } from "vitest"
import type { InfiniteData } from "@tanstack/react-query"
import type { TraceListResponse, TraceSummary } from "@/types"
import { shellQuote, traceRequestUrl, traceToCurl, mergePolledTraces } from "./utils"

describe("shellQuote", () => {
  it("wraps a plain string in single quotes", () => {
    expect(shellQuote("hello")).toBe("'hello'")
  })

  it("escapes embedded single quotes the POSIX way (close, escape, reopen)", () => {
    // Given: a value with an apostrophe, which backslash-escaping inside
    // single quotes would NOT protect (\' is literal backslash + end quote).
    const quoted = shellQuote(`{"name":"O'Brien"}`)

    // Then: the apostrophe is emitted as '\'' so the shell reassembles the
    // original string as one argument.
    expect(quoted).toBe(`'{"name":"O'\\''Brien"}'`)
  })

  it("leaves shell metacharacters inert inside the quotes", () => {
    expect(shellQuote("a $HOME `id` \"x\" ; & | b")).toBe("'a $HOME `id` \"x\" ; & | b'")
  })
})

describe("traceRequestUrl", () => {
  it("uses the endpoint base URL when the trace has no captured host", () => {
    expect(traceRequestUrl("http://localhost:4566", "/queue/foo")).toBe("http://localhost:4566/queue/foo")
  })

  it("strips a trailing slash from the base URL", () => {
    expect(traceRequestUrl("http://localhost:4566/", "/queue/foo")).toBe("http://localhost:4566/queue/foo")
  })

  it("keeps the trace's own host but takes the scheme from the endpoint", () => {
    // Given: a bucket-subdomain host captured on the trace and an http endpoint.
    const url = traceRequestUrl("http://localhost:4566", "/key.txt", "mybucket.s3.localhost:4566")

    // Then: the host (with its port) is preserved and the scheme is http,
    // not guessed from the host string.
    expect(url).toBe("http://mybucket.s3.localhost:4566/key.txt")
  })

  it("uses https when the endpoint is https", () => {
    expect(traceRequestUrl("https://overcast.internal", "/x", "s3.overcast.internal")).toBe(
      "https://s3.overcast.internal/x",
    )
  })
})

describe("traceToCurl", () => {
  it("shell-quotes the URL, headers, and body", () => {
    const curl = traceToCurl(
      {
        method: "POST",
        path: "/2015-03-31/functions",
        host: "localhost:4566",
        requestHeaders: {
          "Content-Type": ["application/json"],
          "X-Weird": ["it's a 'test'"],
          Host: ["localhost:4566"],
          "Content-Length": ["42"],
        },
        requestBody: `{"msg":"don't"}`,
      },
      "http://localhost:4566",
    )

    expect(curl).toContain("curl -X POST")
    expect(curl).toContain(`-H 'Content-Type: application/json'`)
    // Apostrophes in header values survive as one shell argument.
    expect(curl).toContain(`-H 'X-Weird: it'\\''s a '\\''test'\\'''`)
    // Host / Content-Length are managed by curl and skipped.
    expect(curl).not.toContain("Host:")
    expect(curl).not.toContain("Content-Length")
    expect(curl).toContain(`-d '{"msg":"don'\\''t"}'`)
    expect(curl).toContain(`'http://localhost:4566/2015-03-31/functions'`)
  })
})

function summary(id: string): TraceSummary {
  return {
    requestId: id,
    timestamp: "2026-08-08T00:00:00Z",
    method: "GET",
    path: "/",
    service: "s3",
    statusCode: 200,
    duration: 1000,
  }
}

function infinite(...pages: TraceSummary[][]): InfiniteData<TraceListResponse, string | undefined> {
  return {
    pages: pages.map((traces) => ({ traces })),
    pageParams: pages.map((_, i) => (i === 0 ? undefined : `cursor-${i}`)),
  }
}

describe("mergePolledTraces", () => {
  it("prepends new traces to the first page, newest first", () => {
    const old = infinite([summary("b"), summary("a")])

    const merged = mergePolledTraces(old, [summary("d"), summary("c")])

    expect(merged.pages[0].traces.map((t) => t.requestId)).toEqual(["d", "c", "b", "a"])
  })

  it("dedups traces already present in any cached page", () => {
    const old = infinite([summary("c")], [summary("b"), summary("a")])

    const merged = mergePolledTraces(old, [summary("d"), summary("b")])

    expect(merged.pages[0].traces.map((t) => t.requestId)).toEqual(["d", "c"])
    // Older pages are untouched.
    expect(merged.pages[1].traces.map((t) => t.requestId)).toEqual(["b", "a"])
  })

  it("returns the same object when the poll brought nothing new, so no re-render occurs", () => {
    const old = infinite([summary("a")])

    expect(mergePolledTraces(old, [])).toBe(old)
    expect(mergePolledTraces(old, [summary("a")])).toBe(old)
  })

  it("fills an empty first page so an initially empty list goes live", () => {
    const old = infinite([])

    const merged = mergePolledTraces(old, [summary("a")])

    expect(merged.pages[0].traces.map((t) => t.requestId)).toEqual(["a"])
  })
})
