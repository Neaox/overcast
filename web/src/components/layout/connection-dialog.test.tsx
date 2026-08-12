import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { http, HttpResponse } from "msw"
import { server } from "@/test/server"
import { render, screen } from "@/test/render"
import { endpointStore } from "@/services/endpoint-store"
import { ConnectionDialogContent } from "./connection-dialog"

const CUSTOM = { baseUrl: "http://myhost:9999", region: "eu-west-1", label: "Mine" }

describe("ConnectionDialogContent", () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    // The dialog's validation probe polls <baseUrl>/_overcast/info.
    server.use(
      http.get(`${CUSTOM.baseUrl}/_overcast/info`, () => HttpResponse.json({ version: "test" })),
    )
  })

  afterEach(() => {
    endpointStore.reset()
  })

  it("seeds the form from the default endpoint when nothing is configured", () => {
    render(<ConnectionDialogContent />)
    expect(screen.getByDisplayValue("http://localhost:4566")).toBeInTheDocument()
  })

  describe("reopened with a configured custom endpoint", () => {
    beforeEach(() => {
      endpointStore.set(CUSTOM, { explicit: true })
    })

    // Seeding from DEFAULT_ENDPOINT showed localhost:4566 to a user who had
    // configured a custom endpoint — and clicking Connect reverted it.
    it("shows the active endpoint, not the default", () => {
      render(<ConnectionDialogContent />)
      expect(screen.getByDisplayValue(CUSTOM.baseUrl)).toBeInTheDocument()
      expect(screen.getByDisplayValue(CUSTOM.label)).toBeInTheDocument()
    })

    it("keeps the configured endpoint when Connect is clicked without edits", async () => {
      const { user } = render(<ConnectionDialogContent />)
      await user.click(await screen.findByRole("button", { name: "Connect" }))
      expect(endpointStore.get().baseUrl).toBe(CUSTOM.baseUrl)
      expect(endpointStore.get().label).toBe(CUSTOM.label)
    })
  })
})
