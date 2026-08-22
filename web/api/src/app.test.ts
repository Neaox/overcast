import { describe, expect, it, vi, beforeEach, afterEach } from "vitest"
import { app } from "./app.js"
import { GO_BFF_ENDPOINT } from "./service-discovery.js"

/**
 * The dev BFF (this app) has exactly one job: forward /api/* to the Go BFF
 * (internal/bff/bff.go) verbatim. These tests stand in for the Go binary with
 * a mocked `fetch` so they can assert exactly what got forwarded, without a
 * running overcast — the actual end-to-end proof (a live overcast answering
 * a real proxied request) is manual/CI, documented in the PR.
 *
 * The motivating case (#1104): the CloudFormation stack-diagnostics route
 * (PR #1005) was added to the Go BFF only. Before this proxy existed, `pnpm
 * dev` had no mirror for it at all and 404'd; the fix isn't "add the mirror"
 * (that's the disease), it's "stop mirroring" — any route that exists in the
 * Go BFF now works here for free.
 */

const fetchMock = vi.fn()

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock)
})

afterEach(() => {
  fetchMock.mockReset()
  vi.unstubAllGlobals()
})

describe("GET /api/cloudformation/stacks/:stackName/diagnostics (Go-only route)", () => {
  it("forwards to the Go BFF and passes the response through unchanged", async () => {
    const payload = { stackName: "MyStack", operation: "CREATE", resources: [] }
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify(payload), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    )

    const res = await app.request("/api/cloudformation/stacks/MyStack/diagnostics", {
      headers: { "x-overcast-endpoint": "http://localhost:4566", "x-overcast-region": "us-east-1" },
    })

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual(payload)

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [target, init] = fetchMock.mock.calls[0] as [URL, RequestInit]
    expect(target.toString()).toBe(
      `${GO_BFF_ENDPOINT}/api/cloudformation/stacks/MyStack/diagnostics`,
    )
    expect(init.method).toBe("GET")
    const headers = init.headers as Headers
    expect(headers.get("x-overcast-endpoint")).toBe("http://localhost:4566")
    expect(headers.get("x-overcast-region")).toBe("us-east-1")
  })

  it("passes through a 404 (a stack that has never failed) rather than translating it", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "no deploy diagnostics for MyStack" }), {
        status: 404,
        headers: { "Content-Type": "application/json" },
      }),
    )

    const res = await app.request("/api/cloudformation/stacks/MyStack/diagnostics")
    expect(res.status).toBe(404)
    expect(await res.json()).toEqual({ error: "no deploy diagnostics for MyStack" })
  })
})

describe("proxy request forwarding", () => {
  it("forwards method, query string and body for a mutation", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }))

    const res = await app.request("/api/lambda/functions/demo/test-events/foo?region=x", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ event: { hello: "world" } }),
    })

    expect(res.status).toBe(204)
    const [target, init] = fetchMock.mock.calls[0] as [URL, RequestInit]
    expect(target.pathname).toBe("/api/lambda/functions/demo/test-events/foo")
    expect(target.search).toBe("?region=x")
    expect(init.method).toBe("PUT")
    expect(await new Response(init.body as ReadableStream).text()).toBe(
      JSON.stringify({ event: { hello: "world" } }),
    )
  })

  it("streams an SSE response through without buffering", async () => {
    const chunks = ["event: progress\ndata: 1\n\n", "event: result\ndata: {}\n\n"]
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        const encoder = new TextEncoder()
        for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
        controller.close()
      },
    })
    fetchMock.mockResolvedValueOnce(
      new Response(stream, {
        status: 200,
        headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" },
      }),
    )

    const res = await app.request("/api/events")
    expect(res.headers.get("Content-Type")).toBe("text/event-stream")
    expect(await res.text()).toBe(chunks.join(""))
  })

  it("strips hop-by-hop / framing headers in both directions", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response("{}", {
        status: 200,
        headers: {
          "Content-Type": "application/json",
          Connection: "keep-alive",
          "Transfer-Encoding": "chunked",
        },
      }),
    )

    const res = await app.request("/api/health", {
      headers: { Connection: "keep-alive", Host: "example.com" },
    })

    expect(res.headers.get("connection")).toBeNull()
    expect(res.headers.get("transfer-encoding")).toBeNull()

    const [, init] = fetchMock.mock.calls[0] as [URL, RequestInit]
    const headers = init.headers as Headers
    expect(headers.get("host")).toBeNull()
    expect(headers.get("connection")).toBeNull()
  })

  it("returns 502 OvercastUnavailable when the Go BFF cannot be reached", async () => {
    const err = new Error("connect ECONNREFUSED") as NodeJS.ErrnoException
    err.code = "ECONNREFUSED"
    fetchMock.mockRejectedValueOnce(err)

    const res = await app.request("/api/health")
    expect(res.status).toBe(502)
    expect((await res.json()) as { error: string }).toMatchObject({ error: "OvercastUnavailable" })
  })
})
