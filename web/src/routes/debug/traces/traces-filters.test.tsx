/**
 * Filter behaviour of the Request Traces page, driven through the real file
 * route tree and in-memory history — the two things a pure unit test cannot
 * check are exactly the ones the feature is about: that a multi-select emits
 * repeated query params to the server, and that filter edits *replace* the
 * history entry so Back leaves the page instead of walking backwards through
 * every tick of a checkbox.
 */
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { http, HttpResponse } from "msw"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, render, screen, userEvent, waitFor } from "@/test/render"
import { server } from "@/test/server"
import { routeTree } from "@/routeTree.gen"
import type { TraceSummary } from "@/types"

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

vi.mock("@/components/layout/app-shell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => children,
}))

const TRACE: TraceSummary = {
  requestId: "req-1",
  timestamp: "2026-08-09T10:00:00.000Z",
  method: "GET",
  path: "/very/long/path/that/would/otherwise/stretch/the/table/off/screen",
  service: "s3",
  operation: "ListBuckets",
  statusCode: 404,
  duration: 1_500_000,
}

/** Query strings of every trace-list request, newest last. */
function captureTraceRequests(): string[] {
  const seen: string[] = []
  server.use(
    http.get("http://localhost:4566/_overcast/info", () =>
      HttpResponse.json({ region: "us-east-1", account_id: "000000000000", version: "test", debug: true }),
    ),
    http.get("/api/debug/traces/count", () => HttpResponse.json({ count: 1, capacity: 100 })),
    http.get("/api/debug/traces", ({ request }) => {
      const search = new URL(request.url).search
      seen.push(search)
      // The poll asks for traces newer than the newest one; answering with the
      // same trace forever would loop, so only the list request returns data.
      return HttpResponse.json({ traces: search.includes("before=") ? [] : [TRACE] })
    }),
  )
  return seen
}

function renderTraces(initialEntry = "/debug/traces") {
  const queryClient = createTestQueryClient()
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const result = render(<RouterProvider router={router} />, { queryClient })
  return { ...result, router }
}

/** Open a filter dropdown by its trigger label and tick the named boxes. */
async function tick(user: ReturnType<typeof userEvent.setup>, trigger: string, ...labels: string[]) {
  await user.click(await screen.findByRole("button", { name: new RegExp(trigger) }))
  for (const label of labels) {
    await user.click(await screen.findByRole("checkbox", { name: label }))
  }
}

describe("Request Traces filters", () => {
  it("sends every ticked status as its own repeated query param", async () => {
    // Given: the page is showing traces with no filter applied.
    const requests = captureTraceRequests()
    const user = userEvent.setup()
    const { router } = renderTraces()
    expect(await screen.findByText(TRACE.requestId)).toBeInTheDocument()

    // When: the user asks for all errors — 4xx *and* 5xx.
    await tick(user, "all statuses", "4xx", "5xx")

    // Then: the server is asked for both, as repeated params (match-any), and
    // the selection is canonically ordered in the URL regardless of tick order.
    await waitFor(() => {
      expect(requests.at(-1)).toContain("status=4xx&status=5xx")
    })
    expect(router.state.location.search).toMatchObject({ status: ["4xx", "5xx"] })
  })

  it("restores every filter from the URL, including a hand-written one", async () => {
    captureTraceRequests()
    renderTraces("/debug/traces?status=5xx,4xx&method=get&showInternal=true&search=list")

    // Comma-separated and lower-case values are accepted and canonicalised
    // rather than rejected, so a shared or hand-edited link still works.
    expect(await screen.findByRole("button", { name: /2 selected/ })).toBeInTheDocument()
    expect(await screen.findByDisplayValue("list")).toBeInTheDocument()
  })

  it("ignores a malformed filter instead of failing to render", async () => {
    captureTraceRequests()
    renderTraces("/debug/traces?status=6xx&method=%3Cscript%3E")

    expect(await screen.findByText(TRACE.requestId)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /all statuses/ })).toBeInTheDocument()
  })

  it("replaces the history entry for every filter edit, so Back leaves the page", async () => {
    const requests = captureTraceRequests()
    const user = userEvent.setup()
    // Given: the user arrived at the traces page from somewhere else.
    const { router } = renderTraces("/")
    await router.navigate({ to: "/debug/traces" })
    expect(await screen.findByText(TRACE.requestId)).toBeInTheDocument()
    const entriesBefore = router.history.length

    // When: several filters are changed.
    await tick(user, "all statuses", "4xx", "5xx")
    await tick(user, "all methods", "GET")
    await user.click(screen.getByRole("checkbox", { name: /Hide internal/i }))
    await waitFor(() => {
      expect(requests.at(-1)).toContain("method=GET")
    })

    // Then: they all landed on the same history entry.
    expect(router.history.length).toBe(entriesBefore)
    expect(router.state.location.search).toMatchObject({
      status: ["4xx", "5xx"],
      method: ["GET"],
      showInternal: true,
    })

    // And: opening a trace pushes, so one Back returns to the filtered list…
    await user.click(screen.getByText(TRACE.requestId))
    await waitFor(() => {
      expect(router.state.location.pathname).toBe(`/debug/traces/${TRACE.requestId}`)
    })
    router.history.back()
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/debug/traces")
    })
    expect(router.state.location.search).toMatchObject({ status: ["4xx", "5xx"], method: ["GET"] })

    // …and a second Back leaves the traces page entirely rather than stepping
    // backwards through the filter edits.
    router.history.back()
    await waitFor(() => {
      expect(router.state.location.pathname).toBe("/")
    })
  })

  it("debounces the search box into a single navigation", async () => {
    captureTraceRequests()
    const user = userEvent.setup()
    const { router } = renderTraces("/debug/traces")
    expect(await screen.findByText(TRACE.requestId)).toBeInTheDocument()
    const entriesBefore = router.history.length

    // By accessible name, not by placeholder: the placeholder states what
    // search currently covers and is rewritten every time that grows, which is
    // how this test came to be looking for a box that no longer described
    // itself that way.
    await user.type(await screen.findByRole("textbox", { name: /search traces/i }), "bucket")

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ search: "bucket" })
    })
    // Six keystrokes, one settled value, and no extra history entries.
    expect(router.history.length).toBe(entriesBefore)
  })
})
