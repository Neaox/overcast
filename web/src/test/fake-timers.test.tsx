import { useEffect, useState } from "react"
import { describe, expect, it, vi } from "vitest"
import { act, render, screen } from "@/test/render"
import { useFakeTimers } from "@/test/fake-timers"

/**
 * Both assertions here hang forever without the bridge `useFakeTimers` installs,
 * so this doubles as the regression test for it: a plain `vi.useFakeTimers()`
 * fails these by timing out rather than by failing an expectation.
 */
const REVEAL_AFTER_MS = 100

function DelayedReveal() {
  const [armed, setArmed] = useState(false)
  const [revealed, setRevealed] = useState(false)

  useEffect(() => {
    if (!armed) return
    const timer = setTimeout(() => setRevealed(true), REVEAL_AFTER_MS)
    return () => clearTimeout(timer)
  }, [armed])

  return (
    <div>
      <button type="button" onClick={() => setArmed(true)}>
        Arm
      </button>
      {revealed && <p>revealed</p>}
    </div>
  )
}

describe("useFakeTimers", () => {
  useFakeTimers()

  it("lets an awaited user-event interaction settle on the frozen clock", async () => {
    const { user } = render(<DelayedReveal />)

    await user.click(screen.getByRole("button", { name: "Arm" }))

    expect(screen.queryByText("revealed")).not.toBeInTheDocument()
  })

  it("still hands the test explicit control of the clock", async () => {
    const { user } = render(<DelayedReveal />)
    await user.click(screen.getByRole("button", { name: "Arm" }))

    act(() => {
      vi.advanceTimersByTime(REVEAL_AFTER_MS)
    })

    expect(screen.getByText("revealed")).toBeInTheDocument()
  })

  it("lets findBy* drive the clock forward on its own", async () => {
    const { user } = render(<DelayedReveal />)
    await user.click(screen.getByRole("button", { name: "Arm" }))

    expect(await screen.findByText("revealed")).toBeInTheDocument()
  })
})

describe("useFakeTimers cleanup", () => {
  it("leaves no jest global behind for suites that never opted in", () => {
    expect("jest" in globalThis).toBe(false)
  })

  it("restores the real clock", () => {
    expect(Object.prototype.hasOwnProperty.call(setTimeout, "clock")).toBe(false)
  })
})
