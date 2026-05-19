/**
 * Custom render + userEvent helpers for component tests.
 *
 * ## Why a custom render?
 *
 * Components that use TanStack Query hooks need a QueryClient in the React
 * tree. This wrapper provides a fresh, isolated QueryClient per test so that
 * no state leaks between tests.
 *
 * ## Usage
 *
 * ### Basic render (replaces @testing-library/react `render`)
 *
 *   import { render, screen } from "@/test/render"
 *
 *   it("shows the heading", () => {
 *     render(<MyComponent />)
 *     expect(screen.getByRole("heading", { name: "My heading" })).toBeInTheDocument()
 *   })
 *
 * ### Seeding the query cache (preferred over MSW for unit tests)
 *
 * Use `renderWithData` when you want to pre-populate specific query keys
 * without a network round-trip:
 *
 *   import { renderWithData } from "@/test/render"
 *   import { myListQueryOptions } from "@/features/my-service/data"
 *
 *   it("renders the list", () => {
 *     renderWithData(
 *       <MyList />,
 *       [[myListQueryOptions().queryKey, [{ id: "1", name: "Thing" }]]],
 *     )
 *     expect(screen.getByText("Thing")).toBeInTheDocument()
 *   })
 *
 * ### userEvent
 *
 *   import { render, userEvent, screen } from "@/test/render"
 *
 *   it("submits the form", async () => {
 *     const { user } = render(<MyForm />)
 *     await user.click(screen.getByRole("button", { name: "Submit" }))
 *   })
 *
 * ### MSW overrides (preferred for integration/BFF-level tests)
 *
 *   import { server } from "@/test/server"
 *   import { http, HttpResponse } from "msw"
 *
 *   describe("error state", () => {
 *     beforeEach(() => server.use(
 *       http.get("/api/s3/buckets", () => new HttpResponse(null, { status: 500 }))
 *     ))
 *   })
 *
 * ### Memory router (for components that use TanStack Router hooks)
 *
 *   import { renderWithRouter } from "@/test/render"
 *
 *   it("reads params from the URL", () => {
 *     renderWithRouter(RepositoryDetail, {
 *       path: "/ecr/$name",
 *       initialEntry: "/ecr/api",
 *     })
 *     expect(screen.getByText("api")).toBeInTheDocument()
 *   })
 */

import React from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { render as rtlRender, type RenderOptions, type RenderResult } from "@testing-library/react"
import userEvent from "@testing-library/user-event"

// Re-export everything from RTL so tests only need one import.
export * from "@testing-library/react"
export { userEvent }

// ─── QueryClient factory ──────────────────────────────────────────────────

/**
 * Create a fresh QueryClient per test with retry disabled to avoid
 * slow tests caused by query retries on simulated errors.
 */
export function createTestQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        // Prevent stale-while-revalidate from triggering extra fetches
        staleTime: Infinity,
      },
      mutations: {
        retry: false,
      },
    },
  })
}

// ─── Wrapper ─────────────────────────────────────────────────────────────

interface WrapperProps {
  children: React.ReactNode
  queryClient?: QueryClient
}

function AllProviders({ children, queryClient }: WrapperProps) {
  return (
    <QueryClientProvider client={queryClient ?? createTestQueryClient()}>
      {children}
    </QueryClientProvider>
  )
}

// ─── render ──────────────────────────────────────────────────────────────

interface CustomRenderOptions extends Omit<RenderOptions, "wrapper"> {
  queryClient?: QueryClient
}

interface CustomRenderResult extends RenderResult {
  user: ReturnType<typeof userEvent.setup>
  queryClient: QueryClient
}

/**
 * Drop-in replacement for `@testing-library/react`'s `render`.
 * Wraps the component in a `QueryClientProvider` and returns a
 * pre-configured `userEvent` instance alongside the usual RTL result.
 */
export function render(
  ui: React.ReactElement,
  options: CustomRenderOptions = {},
): CustomRenderResult {
  const { queryClient: qc, ...rest } = options
  const queryClient = qc ?? createTestQueryClient()

  const result = rtlRender(ui, {
    wrapper: ({ children }) => <AllProviders queryClient={queryClient}>{children}</AllProviders>,
    ...rest,
  })

  return {
    ...result,
    user: userEvent.setup(),
    queryClient,
  }
}

// ─── renderWithData ───────────────────────────────────────────────────────

type QuerySeed = [queryKey: readonly unknown[], data: unknown]

/**
 * Render with pre-seeded query cache entries.
 *
 * Prefer this over mocking `useQuery` when testing components that rely
 * on TanStack Query. The cache is seeded synchronously before the first
 * render so components see data immediately.
 *
 * @param ui - The React element to render
 * @param seeds - Array of [queryKey, data] pairs to seed into the cache
 * @param options - Custom render options (excluding queryClient)
 */
export function renderWithData(
  ui: React.ReactElement,
  seeds: QuerySeed[],
  options: Omit<CustomRenderOptions, "queryClient"> = {},
): CustomRenderResult {
  const queryClient = createTestQueryClient()

  for (const [queryKey, data] of seeds) {
    queryClient.setQueryData(queryKey, data)
  }

  return render(ui, { ...options, queryClient })
}

// ─── renderWithRouter ─────────────────────────────────────────────────────

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type AnyRouter = ReturnType<typeof createRouter<any, any>>

interface RouterRenderOptions extends Omit<CustomRenderOptions, "wrapper"> {
  /**
   * Route path pattern for the component, e.g. `"/ecr/$name"`.
   * Defaults to `"/"`.
   */
  path?: string
  /**
   * The URL to navigate to on first render.
   * Required when `path` contains params (e.g. `"/ecr/$name"` → `"/ecr/api"`).
   * Defaults to `path`.
   */
  initialEntry?: string
}

interface RouterRenderResult extends CustomRenderResult {
  router: AnyRouter
}

/**
 * Render a component inside a real TanStack Router backed by in-memory history.
 *
 * Use this instead of `vi.mock("@tanstack/react-router")` when the component
 * under test calls router hooks (`useNavigate`, `useParams`, `useSearch`, …).
 *
 * The component is mounted as the sole route at `path`. When the path contains
 * dynamic segments, supply `initialEntry` with a concrete URL:
 *
 * @example
 * ```ts
 * renderWithRouter(RepositoryDetail, {
 *   path: "/ecr/$name",
 *   initialEntry: "/ecr/api",
 * })
 * expect(screen.getByText("api")).toBeInTheDocument()
 * ```
 *
 * To assert navigation side-effects, inspect `router.state.location.pathname`
 * after the interaction:
 *
 * @example
 * ```ts
 * const { user, router } = renderWithRouter(DeleteButton, { path: "/ecr/$name", initialEntry: "/ecr/api" })
 * await user.click(screen.getByRole("button", { name: "Delete" }))
 * expect(router.state.location.pathname).toBe("/ecr")
 * ```
 */
export function renderWithRouter(
  component: React.FC,
  options: RouterRenderOptions = {},
): RouterRenderResult {
  const { path = "/", initialEntry, queryClient: qc, ...rest } = options
  const queryClient = qc ?? createTestQueryClient()

  const rootRoute = createRootRoute()
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path,
    component,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [initialEntry ?? path] }),
  })

  const result = rtlRender(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
    rest,
  )

  return {
    ...result,
    user: userEvent.setup(),
    queryClient,
    router,
  }
}
