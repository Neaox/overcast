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
  services: ["s3", "sqs", "ecr", "sns"],
  serviceTiers: {
    s3: "full",
    sqs: "full",
    // Implemented but switched off — emulated, just not running.
    dynamodb: "full",
    ecr: "partial",
    // Switched on but unimplemented — not emulated, but still reachable.
    sns: "stub",
    lambda: "unsupported",
  },
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

function findSection(name: string) {
  return screen.findByRole("region", { name })
}

describe("Dashboard", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("lists a fully emulated service in the fully-emulated section", async () => {
    renderDashboard()

    expect(within(await findSection("fully emulated")).getByText("S3")).toBeInTheDocument()
  })

  it("renders the services table once the list view is selected", async () => {
    const { user } = renderDashboard()

    await user.click(await screen.findByRole("button", { name: "List view" }))

    expect(screen.getByRole("table", { name: "Services" })).toBeInTheDocument()
  })

  it("groups a partially emulated service by its tier, not by being switched on", async () => {
    renderDashboard()

    expect(within(await findSection("partially emulated")).getByText("ECR")).toBeInTheDocument()
    expect(within(await findSection("not emulated")).queryByText("ECR")).not.toBeInTheDocument()
  })

  it("keeps a disabled but emulated service out of the not-emulated section", async () => {
    renderDashboard()

    expect(within(await findSection("fully emulated")).getByText("DynamoDB")).toBeInTheDocument()
    expect(
      within(await findSection("not emulated")).queryByText("DynamoDB"),
    ).not.toBeInTheDocument()
  })

  it("renders a disabled service as inert rather than a link", async () => {
    renderDashboard()

    const section = await findSection("fully emulated")
    expect(within(section).getByText("DynamoDB").closest("a")).toBeNull()
    expect(within(section).getByText("S3").closest("a")).not.toBeNull()
  })

  it("files an unimplemented service under not emulated", async () => {
    renderDashboard()

    expect(within(await findSection("not emulated")).getByText("Lambda")).toBeInTheDocument()
  })

  it("keeps an enabled not-emulated service reachable from both views", async () => {
    const { user } = renderDashboard()

    expect(
      within(await findSection("not emulated"))
        .getByText("SNS")
        .closest("a"),
    ).not.toBeNull()

    await user.click(await screen.findByRole("button", { name: "List view" }))

    const table = screen.getByRole("table", { name: "Services" })
    expect(within(table).getByText("SNS").closest("a")).not.toBeNull()
  })
})
