import { useRef } from "react"
import { render, screen } from "@/test/render"
import { useScrollTrigger } from "./use-scroll-trigger"

afterEach(() => vi.unstubAllGlobals())

describe("useScrollTrigger", () => {
  it("observes against the supplied scroll container", () => {
    let observedRoot: Element | Document | null | undefined
    class IntersectionObserverMock {
      constructor(_callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
        observedRoot = options?.root
      }

      observe(): void {}
      disconnect(): void {}
    }
    vi.stubGlobal("IntersectionObserver", IntersectionObserverMock)

    function Harness() {
      const rootRef = useRef<HTMLDivElement>(null)
      const sentinelRef = useScrollTrigger({ onTrigger: vi.fn(), rootRef })
      return (
        <div ref={rootRef} data-testid="scroll-root">
          <div ref={sentinelRef} />
        </div>
      )
    }

    render(<Harness />)

    expect(observedRoot).toBe(screen.getByTestId("scroll-root"))
  })
})
