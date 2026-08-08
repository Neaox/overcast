import { createFileRoute, Outlet } from "@tanstack/react-router"

export const Route = createFileRoute("/debug/traces")({
  component: function DebugTracesLayout() {
    return <Outlet />
  },
})
