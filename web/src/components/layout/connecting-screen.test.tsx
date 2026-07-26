import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { act, render, screen } from "@/test/render"
import { ConnectingScreen, RETRY_AFTER_MS } from "./connecting-screen"

describe("ConnectingScreen", () => {
  it("names the configured host in the machine line", () => {
    render(<ConnectingScreen host="emulator.test:9000" onRetry={() => {}} />)
    expect(screen.getByText("connecting to emulator.test:9000")).toBeInTheDocument()
  })

  it("explains that a first boot is slow", () => {
    render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
    expect(
      screen.getByText("Reading emulator state — first boot can take a few seconds."),
    ).toBeInTheDocument()
  })

  it("announces the loader as Connecting", () => {
    render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
    expect(screen.getByRole("img", { name: "Connecting" })).toBeInTheDocument()
  })

  it("drops the region select, theme toggle and search from the disconnected topbar", () => {
    render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
    expect(screen.queryByRole("button", { name: /theme/i })).not.toBeInTheDocument()
    expect(screen.queryByRole("combobox")).not.toBeInTheDocument()
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument()
  })

  function retryChip() {
    return screen.queryByRole("button", { name: "Retry connecting" })
  }

  describe("retry affordance timing", () => {
    beforeEach(() => vi.useFakeTimers())
    afterEach(() => vi.useRealTimers())

    function elapse(ms: number) {
      act(() => {
        vi.advanceTimersByTime(ms)
      })
    }

    it("stays hidden while the attempt is still young", () => {
      render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
      elapse(RETRY_AFTER_MS - 1)
      expect(retryChip()).not.toBeInTheDocument()
    })

    it("appears once the attempt has run for the full delay", () => {
      render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
      elapse(RETRY_AFTER_MS)
      expect(retryChip()).toBeInTheDocument()
    })

    it("labels itself with the delay it waited out", () => {
      render(<ConnectingScreen host="localhost:4566" onRetry={() => {}} />)
      elapse(RETRY_AFTER_MS)
      expect(retryChip()).toHaveTextContent("still working after 5s · retry")
    })
  })

  // user-event drives a real pointer sequence and deadlocks against Vitest's
  // fake clock, so these shorten the delay instead of freezing time.
  describe("retry affordance interaction", () => {
    const BRIEF_DELAY_MS = 20

    function renderBriefly(onRetry: () => void = () => {}) {
      return render(
        <ConnectingScreen host="localhost:4566" onRetry={onRetry} retryAfterMs={BRIEF_DELAY_MS} />,
      )
    }

    it("re-issues the probe when the chip itself is clicked", async () => {
      const onRetry = vi.fn()
      const { user } = renderBriefly(onRetry)
      await user.click(await screen.findByRole("button", { name: "Retry connecting" }))
      expect(onRetry).toHaveBeenCalledOnce()
    })

    it("withdraws the chip while the fresh attempt runs", async () => {
      const { user } = renderBriefly()
      await user.click(await screen.findByRole("button", { name: "Retry connecting" }))
      expect(retryChip()).not.toBeInTheDocument()
    })

    it("re-arms the timer so a stalled retry offers the chip again", async () => {
      const { user } = renderBriefly()
      await user.click(await screen.findByRole("button", { name: "Retry connecting" }))
      expect(await screen.findByRole("button", { name: "Retry connecting" })).toBeInTheDocument()
    })
  })
})
