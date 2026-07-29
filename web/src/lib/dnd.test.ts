import { describe, expect, it } from "vitest"
import type { Modifier } from "@dnd-kit/core"
import { restrictToVerticalAxis } from "./dnd"

/** The modifier only reads `transform`; the rest of the argument is inert here. */
function applyTransform(x: number, y: number) {
  const args = { transform: { x, y, scaleX: 1, scaleY: 1 } } as Parameters<Modifier>[0]
  return restrictToVerticalAxis(args)
}

describe("restrictToVerticalAxis", () => {
  it("drops horizontal movement so a drag cannot widen its scroll container", () => {
    expect(applyTransform(400, 0).x).toBe(0)
  })

  it("passes vertical movement through untouched", () => {
    expect(applyTransform(400, 104).y).toBe(104)
  })

  it("preserves the scale factors dnd-kit supplies", () => {
    expect(applyTransform(400, 104)).toMatchObject({ scaleX: 1, scaleY: 1 })
  })
})
