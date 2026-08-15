import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { renderWithRouter, screen } from "@/test/render"
import { HeaderActions } from "./header-actions"

/**
 * The topbar tail — region select → theme toggle → settings — must be
 * identical on every page, with settings last. Page-specific or
 * build-specific controls (dev tools, the connection quick-edit) sit to the
 * left of the region select so they never shift the tail.
 *
 * Settings opens the /settings page, so it exists in every build — including
 * bundled ones, where the endpoint is fixed but HTTPS is still configurable.
 * The connection plug is the quick edit from any page, and is hidden in
 * bundled builds because there is nothing there to edit.
 */
describe("HeaderActions > action cluster order", () => {
  // The router mounts asynchronously, so every test awaits the gear first.
  const findGear = () => screen.findByRole("link", { name: "Settings" })
  const themeToggle = () => screen.getByRole("button", { name: /^Switch to .+ mode$/ })
  const regionSelect = () => screen.getByRole("combobox")
  const plug = () => screen.getByRole("button", { name: "Connection settings" })

  const renderHeader = () => renderWithRouter(HeaderActions, { path: "/" })

  it("renders the settings control as a link to the settings page", async () => {
    renderHeader()

    expect(await findGear()).toHaveAttribute("href", "/settings")
  })

  it("places the settings control last in the cluster", async () => {
    renderHeader()

    const gear = await findGear()
    expect(gear.parentElement?.lastElementChild).toBe(gear)
  })

  it("places the theme toggle immediately before the settings control", async () => {
    renderHeader()

    const gear = await findGear()
    expect(gear.previousElementSibling).toBe(themeToggle())
  })

  it("places the region select immediately before the theme toggle", async () => {
    renderHeader()

    await findGear()
    expect(themeToggle().previousElementSibling).toContainElement(regionSelect())
  })

  it("places the developer tools toggle to the left of the region select", async () => {
    renderHeader()

    await findGear()
    const devTools = screen.getByRole("button", { name: "Toggle developer tools" })
    expect(devTools.nextElementSibling).toBe(plug())
  })

  it("keeps the connection quick-edit to the left of the region select", async () => {
    renderHeader()

    await findGear()
    expect(plug().nextElementSibling).toContainElement(regionSelect())
  })

  it("opens the connection modal from the plug without leaving the page", async () => {
    const { user, router } = renderHeader()

    await findGear()
    await user.click(plug())

    expect(screen.getByText("Connect to Overcast")).toBeInTheDocument()
    expect(router.state.location.pathname).toBe("/")
  })

  describe("in a bundled build", () => {
    beforeEach(() => {
      vi.stubEnv("VITE_BUNDLED", "true")
    })

    afterEach(() => {
      vi.unstubAllEnvs()
    })

    it("keeps the settings control — the settings page exists in every build", async () => {
      renderHeader()

      expect(await findGear()).toBeInTheDocument()
    })

    it("drops the connection quick-edit — the endpoint is fixed", async () => {
      renderHeader()

      await findGear()
      expect(screen.queryByRole("button", { name: "Connection settings" })).not.toBeInTheDocument()
    })
  })
})
