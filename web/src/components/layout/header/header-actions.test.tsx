import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@/test/render"
import { HeaderActions } from "./header-actions"

/**
 * The topbar tail — region select → theme toggle → settings — must be
 * identical on every page, with settings last whenever it is shown.
 * Page-specific or build-specific controls sit to the left of the region
 * select so they never shift the tail.
 */
describe("HeaderActions > action cluster order", () => {
  const settings = () => screen.getByRole("button", { name: "Connection settings" })
  const themeToggle = () => screen.getByRole("button", { name: /^Switch to .+ mode$/ })
  const regionSelect = () => screen.getByRole("combobox")

  describe("when the endpoint is user-configurable", () => {
    it("renders the connection settings button", () => {
      render(<HeaderActions />)

      expect(settings()).toBeInTheDocument()
    })

    it("places the connection settings button last in the cluster", () => {
      render(<HeaderActions />)

      expect(settings().parentElement?.lastElementChild).toBe(settings())
    })

    it("places the theme toggle immediately before the connection settings button", () => {
      render(<HeaderActions />)

      expect(settings().previousElementSibling).toBe(themeToggle())
    })

    it("places the region select immediately before the theme toggle", () => {
      render(<HeaderActions />)

      expect(themeToggle().previousElementSibling).toContainElement(regionSelect())
    })

    it("places the developer tools toggle to the left of the region select", () => {
      render(<HeaderActions />)

      const devTools = screen.getByRole("button", { name: "Toggle developer tools" })
      expect(devTools.nextElementSibling).toContainElement(regionSelect())
    })
  })

  describe("when the endpoint is not user-configurable (bundled)", () => {
    beforeEach(() => {
      vi.stubEnv("VITE_BUNDLED", "true")
    })

    afterEach(() => {
      vi.unstubAllEnvs()
    })

    it("still renders the connection settings button", () => {
      render(<HeaderActions />)

      expect(settings()).toBeInTheDocument()
    })

    it("places the connection settings button last in the cluster", () => {
      render(<HeaderActions />)

      expect(settings().parentElement?.lastElementChild).toBe(settings())
    })

    it("still places the region select immediately before the theme toggle", () => {
      render(<HeaderActions />)

      expect(themeToggle().previousElementSibling).toContainElement(regionSelect())
    })
  })
})
