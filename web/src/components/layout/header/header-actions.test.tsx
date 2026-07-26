import { describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import { HeaderActions } from "./header-actions"

/**
 * The topbar tail — region select → theme toggle → settings — must be
 * identical on every page, with settings always present and always last.
 * Page-specific or build-specific controls sit to the left of the region
 * select so they never shift the tail.
 */
describe("HeaderActions > action cluster order", () => {
  const gear = () => screen.getByRole("button", { name: "Change connection" })
  const themeToggle = () => screen.getByRole("button", { name: /^Switch to .+ mode$/ })
  const regionSelect = () => screen.getByRole("combobox")

  it("renders the settings control on every page", () => {
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
