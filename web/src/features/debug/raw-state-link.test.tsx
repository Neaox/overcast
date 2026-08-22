import { describe, expect, it } from "vitest"
import { createTestQueryClient, renderWithRouter, screen, seedServerInfo } from "@/test/render"
import { RawStateLink } from "./raw-state-link"

function RawStateLinkOnly() {
  return <RawStateLink service="sqs" namespace="sqs:queues" stateKey="queue-1" />
}

function renderLink(debug: boolean) {
  const queryClient = createTestQueryClient()
  seedServerInfo(queryClient, { debug })
  return renderWithRouter(RawStateLinkOnly, { queryClient })
}

describe("RawStateLink", () => {
  it("hides the link when debug mode is disabled", () => {
    renderLink(false)

    expect(screen.queryByRole("link", { name: /Raw state/ })).not.toBeInTheDocument()
  })

  it("shows the link when debug mode is enabled", async () => {
    renderLink(true)

    expect(await screen.findByRole("link", { name: /Raw state/ })).toBeInTheDocument()
  })
})
