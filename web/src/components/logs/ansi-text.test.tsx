/**
 * The rendering half of the ANSI fix: `parseAnsi` is tested in
 * `lib/ansi.test.ts`, and this pins what a log row actually puts in the DOM —
 * no escape-sequence litter, colour carried on a span, and highlighting still
 * composing with it.
 */
import { describe, expect, it } from "vitest"
import { render, screen } from "@testing-library/react"
import { AnsiText } from "./ansi-text"

const ESC = String.fromCharCode(27)
const BITNAMI = `${ESC}[38;5;6mwordpress ${ESC}[38;5;5m21:33:37.24 ${ESC}[0m${ESC}[38;5;1mERROR${ESC}[0m ==> WORDPRESS_PASSWORD must be set`

describe("AnsiText", () => {
  it("renders a container's colourised line without the escape sequences", () => {
    const { container } = render(<AnsiText text={BITNAMI} />)
    expect(container.textContent).toBe(
      "wordpress 21:33:37.24 ERROR ==> WORDPRESS_PASSWORD must be set",
    )
    expect(container.textContent).not.toContain("38;5")
    expect(container.textContent).not.toContain(ESC)
  })

  it("colours the runs the sequences described", () => {
    const { container } = render(<AnsiText text={BITNAMI} />)
    const spans = Array.from(container.querySelectorAll("span"))
    expect(spans.map((s) => s.textContent)).toEqual(["wordpress ", "21:33:37.24 ", "ERROR"])
    expect(spans[2]).toHaveStyle({ color: "var(--cat-1)" })
  })

  it("adds no element at all for a message with no sequences", () => {
    const { container } = render(<AnsiText text="Certificate request self-signature ok" />)
    expect(container.querySelectorAll("span")).toHaveLength(0)
    expect(container.textContent).toBe("Certificate request self-signature ok")
  })

  it("does not let a row that never reset its colour bleed into the next", () => {
    // How a virtualised log list actually mounts: sibling rows, each parsing
    // its own event. The colour left open by row one must end with row one.
    const { container } = render(
      <div>
        <pre data-testid="row-1">
          <AnsiText text={`${ESC}[31mstarted red and never reset`} />
        </pre>
        <pre data-testid="row-2">
          <AnsiText text="the next event" />
        </pre>
      </div>,
    )
    const rows = container.querySelectorAll("pre")
    expect(rows[0].querySelectorAll("span")).toHaveLength(1)
    expect(rows[1].querySelectorAll("span")).toHaveLength(0)
    expect(rows[1].textContent).toBe("the next event")
  })

  it("renders a malformed sequence as lost text, not as a broken tree", () => {
    const { container } = render(<AnsiText text={`kept ${ESC}[38;5`} />)
    expect(container.textContent).toBe("kept ")
    expect(container.querySelectorAll("span")).toHaveLength(0)
  })

  it("lets the caller wrap each run, so filter highlighting still works", () => {
    render(
      <AnsiText
        text={BITNAMI}
        renderText={(chunk) =>
          chunk.includes("ERROR") ? <mark>{chunk}</mark> : <span>{chunk}</span>
        }
      />,
    )
    expect(screen.getByText("ERROR").tagName).toBe("MARK")
  })
})
