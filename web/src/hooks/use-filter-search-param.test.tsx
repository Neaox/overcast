/**
 * Round-trips the scaffold's filter-in-the-URL contract through a real
 * router: typing commits `?q=` after the debounce settles, a URL with `q`
 * already set restores the input on mount (the "reload" half of the round
 * trip), and every commit replaces the history entry rather than pushing —
 * see `useFilterSearchParam`'s docblock for why.
 */
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { describe, expect, it } from "vitest"
import { createTestQueryClient, render, screen, userEvent, waitFor } from "@/test/render"
import { useFilterSearchParam } from "./use-filter-search-param"

type Search = { q?: string }

function FilterProbe() {
  const [filter, setFilter] = useFilterSearchParam(indexRoute.useSearch(), indexRoute.useNavigate())
  return (
    <div>
      <input
        aria-label="Filter things…"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      <p>Current filter: {filter || "(none)"}</p>
    </div>
  )
}

const rootRoute = createRootRoute()
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/things",
  validateSearch: (search: Record<string, unknown>): Search => ({
    q: typeof search.q === "string" ? search.q : undefined,
  }),
  component: FilterProbe,
})

function renderProbe(initialEntry = "/things") {
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
  const result = render(<RouterProvider router={router} />, {
    queryClient: createTestQueryClient(),
  })
  return { ...result, router }
}

describe("useFilterSearchParam", () => {
  it("commits typed text to the `q` search param once it settles", async () => {
    const user = userEvent.setup()
    const { router } = renderProbe()

    await user.type(await screen.findByLabelText("Filter things…"), "prod")

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ q: "prod" })
    })
  })

  it("restores the filter from the URL on mount — the reload half of the round trip", async () => {
    renderProbe("/things?q=staging")

    expect(await screen.findByText("Current filter: staging")).toBeInTheDocument()
    expect(screen.getByLabelText("Filter things…")).toHaveValue("staging")
  })

  it("drops the `q` param entirely once the filter is cleared, rather than committing an empty string", async () => {
    const user = userEvent.setup()
    const { router } = renderProbe("/things?q=staging")
    await screen.findByDisplayValue("staging")

    await user.clear(screen.getByLabelText("Filter things…"))

    await waitFor(() => {
      expect(router.state.location.search).toEqual({})
    })
  })

  it("replaces the history entry for every commit, so Back leaves the page in one step", async () => {
    const user = userEvent.setup()
    const { router } = renderProbe()
    const entriesBefore = router.history.length

    const input = await screen.findByLabelText("Filter things…")
    await user.type(input, "a")
    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ q: "a" })
    })
    await user.type(input, "b")
    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ q: "ab" })
    })

    // Two settled commits, still the same history entry.
    expect(router.history.length).toBe(entriesBefore)
  })
})
