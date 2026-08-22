/**
 * Round-trips both halves of the EventBridge page's URL state through the
 * real file route: the selected tab (`tab`) and the active tab's filter
 * (`q`) — the two things #1203 asks for ("filters and selected tabs") on a
 * tabbed page specifically, as opposed to the single-list pages covered by
 * `use-filter-search-param.test.tsx`.
 */
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, render, screen, userEvent, waitFor } from "@/test/render"
import { routeTree } from "@/routeTree.gen"
import { ebBusesQueryOptions, ebRulesQueryOptions } from "@/features/eventbridge/data"

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

vi.mock("@/components/layout/app-shell", () => ({
  AppShell: ({ children }: { children: React.ReactNode }) => children,
}))

function renderEventBridge(initialEntry = "/eventbridge") {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(ebBusesQueryOptions().queryKey, [
    { Name: "default", Arn: "arn:aws:events:us-east-1:1:event-bus/default" },
    { Name: "orders", Arn: "arn:aws:events:us-east-1:1:event-bus/orders" },
  ])
  queryClient.setQueryData(ebRulesQueryOptions().queryKey, [
    { Name: "order-created", EventBusName: "orders", State: "ENABLED" },
  ])

  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const result = render(<RouterProvider router={router} />, { queryClient })
  return { ...result, router }
}

describe("EventBridge page — deep-linkable tab and filter state", () => {
  it("defaults to the Buses tab with no `tab` param", async () => {
    renderEventBridge()
    expect(await screen.findByText("orders")).toBeInTheDocument()
  })

  it("opens directly on the tab and filter named in the URL — the reload/share half of the round trip", async () => {
    renderEventBridge("/eventbridge?tab=rules&q=order")

    expect(await screen.findByDisplayValue("order")).toBeInTheDocument()
    expect(screen.getByText("order-created")).toBeInTheDocument()
  })

  it("commits typed filter text to `q` without disturbing `tab`", async () => {
    const user = userEvent.setup()
    const { router } = renderEventBridge("/eventbridge?tab=rules")

    await user.type(await screen.findByLabelText("Filter rules…"), "created")

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ tab: "rules", q: "created" })
    })
  })

  it("clears the filter param when switching tabs, rather than carrying it into the other tab's box", async () => {
    const user = userEvent.setup()
    const { router } = renderEventBridge("/eventbridge?tab=rules&q=created")
    await screen.findByDisplayValue("created")

    await user.click(screen.getByRole("tab", { name: "Event Buses" }))

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ tab: "buses" })
    })
    expect(router.state.location.search).not.toHaveProperty("q")
    expect(await screen.findByLabelText("Filter buses…")).toHaveValue("")
  })
})
