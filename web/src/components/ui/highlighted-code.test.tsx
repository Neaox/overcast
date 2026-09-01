/**
 * The shared highlight component under the markup presentation — jsdom has no
 * CSS Custom Highlight API, so an unstubbed import IS the markup fallback.
 * The sibling highlighted-code.ranges.test.tsx stubs the API and exercises
 * the ranges presentation.
 */
import { render } from "@testing-library/react"
import { HighlightedCode } from "./highlighted-code"

const JSON_TEXT = '{"ok": true, "n": 3}'

const tokenCount = (container: HTMLElement) => container.querySelectorAll("span.token").length

describe("HighlightedCode under the markup presentation", () => {
  it("renders Prism markup for a settled block", () => {
    const { container } = render(
      <HighlightedCode text={JSON_TEXT} language="json" className="font-mono" />,
    )
    expect(tokenCount(container)).toBeGreaterThan(0)
    expect(container.querySelector("pre")?.className).toBe("font-mono")
    expect(container.textContent).toBe(JSON_TEXT)
  })

  it("renders a plain pre for language null — the caller's don't-highlight policy", () => {
    const { container } = render(
      <HighlightedCode text={JSON_TEXT} language={null} className="font-mono" />,
    )
    expect(tokenCount(container)).toBe(0)
    const pre = container.querySelector("pre")
    expect(pre?.textContent).toBe(JSON_TEXT)
    expect(pre?.className).toBe("font-mono")
  })

  it("defers the markup, hydrates on settle, and never sheds it again", () => {
    const view = render(<HighlightedCode text={JSON_TEXT} language="json" defer />)
    // Deferred: identical text, no spans — the swap can change colour, never
    // the pixels a measured row occupies.
    expect(tokenCount(view.container)).toBe(0)
    expect(view.container.textContent).toBe(JSON_TEXT)

    view.rerender(<HighlightedCode text={JSON_TEXT} language="json" defer={false} />)
    const hydrated = tokenCount(view.container)
    expect(hydrated).toBeGreaterThan(0)

    // The latch: hydrated markup must survive the next scroll's defer=true,
    // or the churn the deferral exists to prevent comes back as flicker.
    view.rerender(<HighlightedCode text={JSON_TEXT} language="json" defer />)
    expect(tokenCount(view.container)).toBe(hydrated)
  })

  it("keeps the same classes on the pre across the deferred and settled branches", () => {
    const deferred = render(
      <HighlightedCode text={JSON_TEXT} language="json" defer className="font-mono text-xs" />,
    )
    const settled = render(
      <HighlightedCode text={JSON_TEXT} language="json" className="font-mono text-xs" />,
    )
    expect(deferred.container.querySelector("pre")?.className).toBe(
      settled.container.querySelector("pre")?.className,
    )
  })

  it("escapes rather than highlights a language with no registered grammar", () => {
    const { container } = render(
      <HighlightedCode text="SELECT <b> FROM t" language="fortran" className="font-mono" />,
    )
    expect(tokenCount(container)).toBe(0)
    expect(container.textContent).toBe("SELECT <b> FROM t")
    expect(container.querySelector("b")).toBeNull()
  })
})
