import { describe, it, expect, beforeEach } from "vitest"
import { applyStoredTheme } from "@/hooks/use-theme"

/**
 * `applyStoredTheme` is the boot-time half of the theme: `useTheme` only runs
 * where it is mounted, and its one mount point (the header) sits behind the
 * connection gate. The console's pre-connection screens have to honour the
 * stored preference too, so the value is applied once from `main.tsx`.
 */
describe("applyStoredTheme", () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.removeAttribute("data-theme")
  })

  it("applies a stored explicit theme to the document element", () => {
    localStorage.setItem("overcast:theme", JSON.stringify("light"))

    applyStoredTheme()

    expect(document.documentElement.getAttribute("data-theme")).toBe("light")
  })

  it("leaves the document on the system theme when nothing is stored", () => {
    applyStoredTheme()

    expect(document.documentElement.hasAttribute("data-theme")).toBe(false)
  })

  it("clears an applied theme when the stored value is 'system'", () => {
    document.documentElement.setAttribute("data-theme", "dark")
    localStorage.setItem("overcast:theme", JSON.stringify("system"))

    applyStoredTheme()

    expect(document.documentElement.hasAttribute("data-theme")).toBe(false)
  })

  it("falls back to the system theme on a corrupt stored value", () => {
    localStorage.setItem("overcast:theme", "{not json")

    expect(() => applyStoredTheme()).not.toThrow()
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false)
  })
})
