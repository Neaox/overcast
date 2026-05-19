import { createFileRoute, redirect } from "@tanstack/react-router"

// Redirect legacy /mail URL to /inbox.
export const Route = createFileRoute("/mail")({
  beforeLoad: () => {
    // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router convention
    throw redirect({ to: "/inbox", replace: true })
  },
  component: () => null,
})
