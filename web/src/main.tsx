import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { createRouter, RouterProvider } from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { ReactQueryDevtools } from "@tanstack/react-query-devtools"
import { routeTree } from "./routeTree.gen"
import { ToastContextProvider } from "@/components/ui/toast"
import "@/styles/global.css"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 1000 * 30,
      retry: 1,
    },
  },
})

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

const root = document.getElementById("root")!
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastContextProvider>
        <RouterProvider router={router} />
      </ToastContextProvider>
      {import.meta.env.DEV && <ReactQueryDevtools buttonPosition="bottom-left" />}
    </QueryClientProvider>
  </StrictMode>,
)
