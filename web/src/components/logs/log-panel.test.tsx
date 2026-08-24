/**
 * `LogPanel`'s fetch wiring: a `LogFilter` in, one `FilterLogEventsCommand`
 * out. CloudWatch Logs goes through the AWS SDK client, not a BFF route, so
 * MSW cannot see these calls — `@/services/aws-clients` is mocked instead and
 * the test asserts on the command the panel actually built (see
 * `msw-cannot-intercept-aws-sdk-in-web-tests` in project memory).
 */
import { act } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen, waitFor } from "@/test/render"
import { useFakeTimers } from "@/test/fake-timers"
import { LogPanel } from "./log-panel"

interface FilterCall {
  logGroupName?: string
  logStreamNames?: string[]
  startTime?: number
  endTime?: number
  filterPattern?: string
  limit?: number
}

const backend = vi.hoisted(() => ({
  calls: [] as FilterCall[],
  events: [] as Array<{ timestamp: number; message: string; logStreamName?: string }>,
}))

vi.mock("@/services/aws-clients", () => ({
  awsClients: {
    logs: () => ({
      send: (command: { constructor: { name: string }; input: FilterCall }) => {
        if (command.constructor.name === "FilterLogEventsCommand") {
          backend.calls.push(command.input)
          return Promise.resolve({ events: backend.events, searchedLogStreams: [] })
        }
        return Promise.resolve({})
      },
    }),
  },
}))

function lastCall(): FilterCall {
  const call = backend.calls.at(-1)
  if (!call) throw new Error("FilterLogEventsCommand was never called")
  return call
}

describe("LogPanel", () => {
  useFakeTimers()

  beforeEach(() => {
    backend.calls = []
    backend.events = []
  })

  it("fetches the pinned group with a default relative window and no pattern", async () => {
    render(<LogPanel pinned={{ group: "/aws/lambda/checkout" }} />)

    await waitFor(() => expect(backend.calls.length).toBeGreaterThan(0))
    const call = lastCall()
    expect(call.logGroupName).toBe("/aws/lambda/checkout")
    expect(call.filterPattern).toBeUndefined()
    expect(call.logStreamNames).toBeUndefined()
    expect(typeof call.startTime).toBe("number")
  })

  it("scopes the request to a pinned stream", async () => {
    render(<LogPanel pinned={{ group: "/aws/lambda/checkout", stream: "2026/08/25/[1]abc" }} />)

    await waitFor(() => expect(backend.calls.length).toBeGreaterThan(0))
    expect(lastCall().logStreamNames).toEqual(["2026/08/25/[1]abc"])
  })

  it("hides the time-range select and pins the window when the page supplies one", async () => {
    render(
      <LogPanel
        pinned={{
          group: "/aws/lambda/checkout",
          stream: "s1",
          time: { kind: "absolute", startMs: 1_000, endMs: 2_000 },
        }}
      />,
    )

    await waitFor(() => expect(backend.calls.length).toBeGreaterThan(0))
    expect(lastCall().startTime).toBe(1_000)
    expect(lastCall().endTime).toBe(2_000)
    expect(screen.queryByLabelText("Time range")).not.toBeInTheDocument()
  })

  it("re-fetches with the selected relative window when the user changes it", async () => {
    const { user } = render(<LogPanel pinned={{ group: "/aws/lambda/checkout" }} />)
    await waitFor(() => expect(backend.calls.length).toBeGreaterThan(0))
    const initialStart = lastCall().startTime!

    const select = screen.getByLabelText("Time range") as HTMLSelectElement
    await user.selectOptions(select, "15m")

    await waitFor(() => expect(lastCall().startTime).not.toBe(initialStart))
    // The 15-minute window is narrower (a larger, more recent `startTime`)
    // than the 1-hour default it replaced.
    expect(lastCall().startTime!).toBeGreaterThan(initialStart)
  })

  it("debounces the filter-pattern input before refetching", async () => {
    const { user } = render(<LogPanel pinned={{ group: "/aws/lambda/checkout" }} />)
    await waitFor(() => expect(backend.calls.length).toBeGreaterThan(0))
    const callsBeforeTyping = backend.calls.length

    const input = screen.getByPlaceholderText(/Filter/)
    await user.type(input, "ERROR")

    // Not yet — the 300ms debounce has not elapsed, so no new request went
    // out for any of the intermediate keystrokes.
    expect(backend.calls.length).toBe(callsBeforeTyping)

    act(() => {
      vi.advanceTimersByTime(300)
    })

    await waitFor(() => expect(lastCall().filterPattern).toBe("ERROR"))
  })
})
