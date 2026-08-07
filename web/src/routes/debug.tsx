import { createFileRoute, Outlet } from "@tanstack/react-router"

export const Route = createFileRoute("/debug")({
  component: function DebugLayout() {
    return <Outlet />
  },
})
