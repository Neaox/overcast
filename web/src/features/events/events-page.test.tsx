import { describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import { EventsPage } from "./events-page"

describe("EventsPage", () => {
  it("renders when event sources are not navigable services", () => {
    render(<EventsPage />)

    expect(screen.getByRole("heading", { name: "Event Stream" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /sources/i })).toBeInTheDocument()
  })

  it("checks non-request sources by default", async () => {
    const { user } = render(<EventsPage />)

    await user.click(screen.getByRole("button", { name: /sources/i }))

    expect(screen.queryByText("Hide requests")).not.toBeInTheDocument()
    expect(screen.getByRole("checkbox", { name: "Requests" })).not.toBeChecked()
    expect(screen.getByRole("checkbox", { name: "Service errors" })).toBeChecked()
  })
})
