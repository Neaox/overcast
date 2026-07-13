import { describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import { EventsPage } from "./events-page"

describe("EventsPage", () => {
  it("renders when event sources are not navigable services", () => {
    render(<EventsPage />)

    expect(screen.getByRole("heading", { name: "Event Stream" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /all sources/i })).toBeInTheDocument()
  })
})
