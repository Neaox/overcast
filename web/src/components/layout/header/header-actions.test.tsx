import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { render, screen } from "@/test/render"
import { HeaderActions } from "./header-actions"

/**
 * The topbar tail — region select → theme toggle → settings — must be
 * identical on every page, with settings last whenever it is shown.
 * Page-specific or build-specific controls sit to the left of the region
 * select so they never shift the tail.
 *
 * Settings is the one tail member that can disappear: a bundled build derives
 * the endpoint from `window.location`, so the control has nothing to reopen.
 * It drops off the end rather than moving, which leaves the order intact.
 */
describe("HeaderActions > action cluster order", () => {
  const gear = () => screen.getByRole("button", { name: "Change connection" })
  const themeToggle = () => screen.getByRole("button", { name: /^Switch to .+ mode$/ })
  const regionSelect = () => screen.getByRole("combobox")

  describe("when the endpoint is user-configurable", () => {
    it("renders the settings control", () => {
      render(<HeaderActions />)

      expect(gear()).toBeInTheDocument()
    })

    it("places the settings control last in the cluster", () => {
      render(<HeaderActions />)

      expect(gear().parentElement?.lastElementChild).toBe(gear())
    })

    it("places the theme toggle immediately before the settings control", () => {
      render(<HeaderActions />)

      expect(gear().previousElementSibling).toBe(themeToggle())
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

  describe("when the endpoint is not user-configurable", () => {
    beforeEach(() => {
      vi.stubEnv("VITE_BUNDLED", "true")
    })

    afterEach(() => {
      vi.unstubAllEnvs()
    })

    it("omits the settings control rather than offering a no-op", () => {
      render(<HeaderActions />)

      expect(screen.queryByRole("button", { name: "Change connection" })).not.toBeInTheDocument()
    })

    it("leaves the theme toggle last in the cluster", () => {
      render(<HeaderActions />)

      expect(themeToggle().parentElement?.lastElementChild).toBe(themeToggle())
    })

    it("still places the region select immediately before the theme toggle", () => {
      render(<HeaderActions />)

      expect(themeToggle().previousElementSibling).toContainElement(regionSelect())
    })
  })
})
