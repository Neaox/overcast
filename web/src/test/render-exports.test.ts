import { describe, expect, it } from "vitest"
import { fireEvent, screen, userEvent } from "@/test/render"

/**
 * Guards `@/test/render`'s own export surface.
 *
 * Every component test imports through this module, but nothing else asserts
 * that its exports actually resolve — and one of them silently did not (see the
 * comment above `export const userEvent` in `render.tsx`). A regression there
 * surfaces as `Cannot read properties of undefined` in whichever unrelated
 * suite happens to reach for the export first, so it is worth pinning here.
 *
 * The `user` handed back by `render` needs no guard of its own: every
 * interaction test in the suite already exercises it.
 */
describe("@/test/render exports", () => {
  it("hands out a usable userEvent rather than an undefined re-export", () => {
    expect(typeof userEvent.setup).toBe("function")
  })

  it("passes RTL's own helpers through the star re-export", () => {
    expect(typeof screen.getByRole).toBe("function")
    expect(typeof fireEvent.click).toBe("function")
  })
})
