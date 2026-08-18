/**
 * Forward paging for a dead-tail GetLogEvents viewer.
 *
 * The contract under test is AWS's own: `nextForwardToken` is always returned,
 * and getting back the *same token you passed* — not an empty page — is the
 * canonical end-of-stream signal. The walk must chain tokens page to page,
 * never run two fetches at once, and stop (rather than spin) on both the
 * end-of-stream signal and a failed fetch.
 */

import { act, renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { logs } from "@/services/api"
import { useForwardLogPages } from "./use-forward-log-pages"

vi.mock("@/services/api", () => ({
  logs: { getEvents: vi.fn() },
}))

const getEvents = vi.mocked(logs.getEvents)

beforeEach(() => getEvents.mockReset())

type Page = Awaited<ReturnType<typeof logs.getEvents>>

function page(messages: string[], forward: string): Page {
  return {
    events: messages.map((message, i) => ({ timestamp: 1_000 + i, message })),
    nextBackwardToken: "b",
    nextForwardToken: forward,
  }
}

/** Run `fn` inside act and let the fetch's microtask chain settle. */
async function actAsync(fn: () => void) {
  await act(async () => {
    fn()
    await Promise.resolve()
  })
}

function renderForward(initial: { group?: string; stream?: string; startToken?: string } = {}) {
  return renderHook(
    ({ group, stream, startToken }: { group?: string; stream?: string; startToken?: string }) =>
      useForwardLogPages({ logGroup: group, logStream: stream, startToken }),
    {
      initialProps: {
        group: initial.group ?? "g",
        stream: initial.stream ?? "s",
        startToken: "startToken" in initial ? initial.startToken : "f0",
      },
    },
  )
}

describe("useForwardLogPages", () => {
  it("walks the forward tokens, appending each page's events in order", async () => {
    getEvents.mockResolvedValueOnce(page(["one"], "f1")).mockResolvedValueOnce(page(["two"], "f2"))
    const { result } = renderForward()

    await actAsync(() => result.current.loadNewer())
    expect(result.current.events.map((e) => e.message)).toEqual(["one"])
    expect(result.current.exhausted).toBe(false)

    await actAsync(() => result.current.loadNewer())
    expect(result.current.events.map((e) => e.message)).toEqual(["one", "two"])

    expect(getEvents).toHaveBeenNthCalledWith(1, "g", "s", { nextToken: "f0", limit: 200 })
    expect(getEvents).toHaveBeenNthCalledWith(2, "g", "s", { nextToken: "f1", limit: 200 })
  })

  it("treats the same token coming back as the end of the stream", async () => {
    // AWS's contract: at the end, GetLogEvents returns the token you passed
    // (with no events). That, not emptiness, is the stop signal.
    getEvents.mockResolvedValueOnce(page([], "f0"))
    const { result } = renderForward()

    await actAsync(() => result.current.loadNewer())
    expect(result.current.exhausted).toBe(true)
    expect(result.current.events).toEqual([])
  })

  it("runs at most one fetch at a time", async () => {
    let resolve!: (p: Page) => void
    getEvents.mockImplementationOnce(() => new Promise<Page>((r) => (resolve = r)))

    const { result } = renderForward()
    act(() => {
      result.current.loadNewer()
      result.current.loadNewer()
    })
    expect(getEvents).toHaveBeenCalledTimes(1)
    expect(result.current.loading).toBe(true)

    await actAsync(() => resolve(page(["one"], "f1")))
    expect(result.current.loading).toBe(false)
    expect(result.current.events.map((e) => e.message)).toEqual(["one"])
  })

  it("starts over when the stream identity changes", async () => {
    getEvents.mockResolvedValueOnce(page(["old stream"], "f1"))
    const { result, rerender } = renderForward()

    await actAsync(() => result.current.loadNewer())
    expect(result.current.events).toHaveLength(1)

    rerender({ group: "g", stream: "other", startToken: "x0" })
    expect(result.current.events).toEqual([])
    expect(result.current.exhausted).toBe(false)

    getEvents.mockResolvedValueOnce(page(["new stream"], "x1"))
    await actAsync(() => result.current.loadNewer())
    expect(result.current.events.map((e) => e.message)).toEqual(["new stream"])
    expect(getEvents).toHaveBeenLastCalledWith("g", "other", { nextToken: "x0", limit: 200 })
  })

  it("stops the walk on a failed fetch instead of spinning", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    getEvents.mockRejectedValueOnce(new TypeError("network error"))
    const { result } = renderForward()

    await actAsync(() => result.current.loadNewer())
    expect(result.current.exhausted).toBe(true)
    expect(result.current.loading).toBe(false)
    expect(warn).toHaveBeenCalled()
  })

  it("does nothing without a start token", async () => {
    const { result } = renderForward({ startToken: undefined })
    await actAsync(() => result.current.loadNewer())
    expect(getEvents).not.toHaveBeenCalled()
  })
})
