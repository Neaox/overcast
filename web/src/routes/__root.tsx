import { createRootRoute, Outlet } from "@tanstack/react-router"
import { TanStackRouterDevtools } from "@tanstack/react-router-devtools"
import { AppShell } from "@/components/layout/app-shell"

export const Route = createRootRoute({
  component: () => (
    <>
      <AppShell>
        <Outlet />
      </AppShell>
      {import.meta.env.DEV && <TanStackRouterDevtools position="bottom-right" />}
    </>
  ),
})
