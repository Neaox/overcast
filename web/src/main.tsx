/* eslint-disable react-refresh/only-export-components */
import { StrictMode, lazy, Suspense, useState, useCallback, useEffect } from "react"
import { createRoot } from "react-dom/client"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { routeTree } from "./routeTree.gen"
import { ToastContextProvider, useToast } from "@/components/ui/toast"
import { DevToolsContext } from "@/hooks/use-dev-tools"
import { endpointStore } from "@/services/endpoint-store"
import { hasPersistedRegion, fetchServerRegion } from "@/services/discovery"
import "@/styles/global.css"

const DevToolsPanel = lazy(() => import("@/components/dev-tools"))

function getErrorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  return "Unknown error"
}

function isNetworkError(error: unknown): boolean {
  if (!(error instanceof Error)) return false
  if (error.name === "NetworkError") return true
  return /failed to fetch|network\s?error|load failed/i.test(error.message)
}

// On startup, seed the region from the server's OVERCAST_DEFAULT_REGION if
// the user has not explicitly chosen a region in this tab session.
if (!hasPersistedRegion()) {
  void fetchServerRegion(endpointStore.get().baseUrl).then((serverRegion) => {
    if (!serverRegion) return
    const current = endpointStore.get()
    if (current.region !== serverRegion) {
      endpointStore.set({ ...current, region: serverRegion })
    }
  })
}

const router = createRouter({
  routeTree,
  context: {},
  defaultPreload: "intent",
})

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router
  }
}

function QueryProvider({ children }: { children: React.ReactNode }) {
  const { toast } = useToast()
  const [queryClient] = useState(
    () =>
      new QueryClient({
        queryCache: new QueryCache({
          onError: (error) => {
            if (!isNetworkError(error)) return
            toast({
              title: "Network error",
              description: getErrorMessage(error),
              variant: "danger",
            })
          },
        }),
        mutationCache: new MutationCache({
          onError: (error) => {
            if (!isNetworkError(error)) return
            toast({
              title: "Network error",
              description: getErrorMessage(error),
              variant: "danger",
            })
          },
        }),
        defaultOptions: {
          queries: {
            staleTime: 1000 * 30,
            retry: 1,
          },
        },
      }),
  )

  // When the active endpoint changes, reset all queries scoped to the
  // previous endpoint so stale data from the old (baseUrl, region) pair is
  // never shown.
  useEffect(() => {
    return endpointStore.subscribe((prev) => {
      void queryClient.resetQueries({ queryKey: [prev.baseUrl, prev.region] })
    })
  }, [queryClient])

  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

function App() {
  const [devToolsOpen, setDevToolsOpen] = useState(false)
  const toggle = useCallback(() => setDevToolsOpen((v) => !v), [])

  return (
    <StrictMode>
      <ToastContextProvider>
        <DevToolsContext value={{ open: devToolsOpen, toggle }}>
          <QueryProvider>
            <RouterProvider router={router} />
            {import.meta.env.DEV && devToolsOpen && (
              <Suspense>
                <DevToolsPanel />
              </Suspense>
            )}
          </QueryProvider>
        </DevToolsContext>
      </ToastContextProvider>
    </StrictMode>
  )
}

const root = document.getElementById("root")!
createRoot(root).render(<App />)
