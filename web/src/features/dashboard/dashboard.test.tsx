import { beforeEach, describe, expect, it } from "vitest"
import { createTestQueryClient, renderWithRouter, screen, within } from "@/test/render"
import { TooltipProvider } from "@/components/ui/tooltip"
import { FavouritesProvider } from "@/hooks/use-favourites"
import type { HealthResponse } from "@/types/common"
import { Dashboard } from "./dashboard"

const HEALTH: HealthResponse = {
  status: "ok",
  timestamp: "2026-07-26T00:00:00Z",
  version: "0.1.0-test",
  services: ["s3", "sqs"],
  serviceTiers: { s3: "full", sqs: "full", lambda: "unsupported" },
  storage: { default: "memory" },
}

function DashboardOnly() {
  return (
    <TooltipProvider>
      <FavouritesProvider>
        <Dashboard />
      </FavouritesProvider>
    </TooltipProvider>
  )
}

function renderDashboard() {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(["health"], HEALTH)
  return renderWithRouter(DashboardOnly, { queryClient })
}

function findInUseSection() {
  return screen.findByRole("region", { name: "in use" })
}

describe("Dashboard", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("lists a fully emulated, enabled service in the in-use section", async () => {
    renderDashboard()

    expect(within(await findInUseSection()).getByText("S3")).toBeInTheDocument()
  })

  it("renders the services table once the list view is selected", async () => {
    const { user } = renderDashboard()

    await user.click(await screen.findByRole("button", { name: "List view" }))

    expect(screen.getByRole("table", { name: "Services" })).toBeInTheDocument()
  })

  it("keeps a service the emulator has not enabled out of the in-use section", async () => {
    renderDashboard()

    expect(within(await findInUseSection()).queryByText("Lambda")).not.toBeInTheDocument()
  })
})
