/**
 * The error screen is the one screen nobody exercises by hand, so what it
 * promises is pinned here: the message is always shown verbatim, the three
 * failure kinds tell their own story, retry actually retries, and the copy
 * button carries enough context to open a bug report with.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest"
import { screen } from "@testing-library/react"
import { renderWithRouter, userEvent } from "@/test/render"
import { ToastContextProvider } from "@/components/ui/toast"
import { endpointStore } from "@/services/endpoint-store"
import { RouteError } from "./route-error"

const reset = vi.fn()

/* The toast provider sits above RouterProvider in main.tsx, so the copy button
   has one at runtime — including when the root route itself fails. */
function renderError(error: Error, at = "/cloudwatch/logs") {
  return renderWithRouter(
    () => (
      <ToastContextProvider>
        <RouteError error={error} reset={reset} info={{ componentStack: "" }} />
      </ToastContextProvider>
    ),
    { path: at, initialEntry: at },
  )
}

beforeEach(() => {
  reset.mockClear()
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe("RouteError", () => {
  it("shows the message verbatim, whatever the error is", async () => {
    renderError(new Error("ValidationException: log group does not exist"))
    expect(
      await screen.findByText("ValidationException: log group does not exist"),
    ).toBeInTheDocument()
  })

  it("names the emulator, not the browser, when the endpoint is unreachable", async () => {
    renderError(new TypeError("Failed to fetch"))
    expect(await screen.findByText("Can't reach the emulator")).toBeInTheDocument()
    expect(screen.getByText(endpointStore.get().baseUrl)).toBeInTheDocument()
  })

  it("tells a stale tab to reload, rather than blaming the emulator", async () => {
    renderError(new Error("Failed to fetch dynamically imported module: /assets/s3-a1b2.js"))
    expect(await screen.findByText("This page didn't finish loading")).toBeInTheDocument()
    expect(screen.queryByText("Can't reach the emulator")).not.toBeInTheDocument()
  })

  it("falls back to a generic story for anything else", async () => {
    renderError(new Error("Cannot read properties of undefined"))
    expect(await screen.findByText("Something went wrong")).toBeInTheDocument()
  })

  it("retries by clearing the boundary and re-running the route", async () => {
    renderError(new Error("boom"))
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /try again/i }))
    expect(reset).toHaveBeenCalledTimes(1)
  })

  it("offers a way out that does not depend on the failed route", async () => {
    renderError(new Error("boom"))
    expect(await screen.findByRole("button", { name: /reload page/i })).toBeInTheDocument()
    expect(screen.getByRole("link", { name: /dashboard/i })).toHaveAttribute("href", "/")
  })

  it("copies a report with the route, endpoint and stack in it", async () => {
    const error = new Error("boom")
    error.stack = "Error: boom\n    at LogGroupList (log-group-list.tsx:42:7)"
    // `userEvent.setup()` installs jsdom's missing clipboard, so the copy goes
    // through `lib/clipboard.ts`'s real async path and lands somewhere readable.
    const { user } = renderError(error)
    await user.click(await screen.findByRole("button", { name: /copy error details/i }))

    const copied = await navigator.clipboard.readText()
    expect(copied).toContain("/cloudwatch/logs")
    expect(copied).toContain(endpointStore.get().baseUrl)
    expect(copied).toContain("Error: boom")
    expect(copied).toContain("log-group-list.tsx:42:7")
  })

  it("keeps the stack behind a disclosure, so it does not shout over the message", async () => {
    const error = new Error("boom")
    error.stack = "Error: boom\n    at somewhere"
    renderError(error)

    const summary = await screen.findByText("Stack trace")
    expect(summary.closest("details")).toBeInTheDocument()
    expect(summary.closest("details")).not.toHaveAttribute("open")
  })

  it("omits the disclosure entirely when there is no stack", async () => {
    const error = new Error("boom")
    error.stack = undefined
    renderError(error)

    expect(await screen.findByText("boom")).toBeInTheDocument()
    expect(screen.queryByText("Stack trace")).not.toBeInTheDocument()
  })

  it("survives a thrown non-Error", async () => {
    // Anything can be thrown in JS, and the router hands it through untouched.
    renderError("just a string" as unknown as Error, "/")
    expect(await screen.findByText("just a string")).toBeInTheDocument()
  })
})
