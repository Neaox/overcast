/**
 * Table mode's row anatomy: a level badge, a click-to-expand body, and the
 * virtualizer re-measuring once a row's height stops being the collapsed
 * constant. Mirrors the pattern `log-events-viewer.test.tsx` uses for the
 * same reason — jsdom gives every element a zero height, so the real
 * `@tanstack/react-virtual` renders no rows at all, and nothing about
 * expansion would be observable without a deterministic stand-in.
 */
import { describe, expect, it, vi } from "vitest"
import { fireEvent, render, screen } from "@/test/render"
import { LogViewer } from "./log-viewer"

const measure = vi.fn()

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({
    count,
    estimateSize,
  }: {
    count: number
    estimateSize: (index: number) => number
  }) => {
    let offset = 0
    const items = Array.from({ length: count }, (_, index) => {
      const size = estimateSize(index)
      const item = { index, key: index, start: offset, end: offset + size }
      offset += size
      return item
    })
    return {
      getTotalSize: () => offset,
      getVirtualItems: () => items,
      measureElement: vi.fn(),
      measure,
      isScrolling: false,
      scrollOffset: 0,
      scrollRect: { height: 800 },
    }
  },
}))

describe("LogViewer table mode", () => {
  it("renders a level badge for a detected level", () => {
    render(
      <LogViewer
        events={[{ message: "ERROR something bad happened", timestamp: 1 }]}
        defaultMode="table"
        showModeToggle={false}
      />,
    )
    expect(screen.getByText("error")).toBeInTheDocument()
  })

  it("starts collapsed at the fixed row height and expands on click", () => {
    const { container } = render(
      <LogViewer
        events={[{ message: "ERROR something bad happened", timestamp: 1 }]}
        defaultMode="table"
        showModeToggle={false}
      />,
    )
    measure.mockClear()

    const row = container.querySelector('[data-index="0"]') as HTMLElement
    expect(row.style.height).toBe("28px")

    fireEvent.click(screen.getByText(/ERROR something bad happened/))

    // The collapsed constant only applies while the row is collapsed — once
    // expanded, the wrapper carries no inline height and lets the (mocked)
    // measured size through instead.
    expect(row.style.height).toBe("")
    // Toggling expansion invalidates the virtualizer's cached sizes, the same
    // nudge `log-events-viewer.tsx`'s collapse mode makes when a row that
    // skipped measurement needs to start being measured.
    expect(measure).toHaveBeenCalled()
  })

  it("does not expand when the click lands on the row's copy button", () => {
    const { container } = render(
      <LogViewer
        events={[{ message: "plain message", timestamp: 1 }]}
        defaultMode="table"
        showModeToggle={false}
      />,
    )
    const row = container.querySelector('[data-index="0"]') as HTMLElement
    const copyButton = screen.getByRole("button", { name: /copy log message/i })
    fireEvent.click(copyButton)
    expect(row.style.height).toBe("28px")
  })

  it("keeps plain mode's row anatomy unchanged", () => {
    render(
      <LogViewer
        events={[{ message: "hello world", timestamp: 1 }]}
        defaultMode="plain"
        showModeToggle={false}
      />,
    )
    // Plain mode never shows a level badge (it tints the row instead) and
    // never gains a copy button — both are table-mode-only additions.
    expect(screen.queryByRole("button", { name: /copy log message/i })).not.toBeInTheDocument()
    expect(screen.getByText(/hello world/)).toBeInTheDocument()
  })
})
