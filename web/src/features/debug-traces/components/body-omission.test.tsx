import { describe, expect, it } from "vitest"
import { render, renderWithRouter, screen } from "@/test/render"
import { BodyOmissionChip, BodyOmissionNotice } from "./body-omission"

// The two are rendered together at every call site, so each test asserts what
// the pair produces — exactly one of them must speak, and the other must be
// silent.
function renderBoth(reason: string | undefined, hasBody: boolean, ownRequestId?: string) {
  return render(
    <>
      <BodyOmissionChip reason={reason} hasBody={hasBody} />
      <BodyOmissionNotice reason={reason} hasBody={hasBody} ownRequestId={ownRequestId} />
    </>,
  )
}

describe("body omission", () => {
  it("says nothing when the body was never lost", () => {
    // A request that carried no body must not be described as one we dropped.
    renderBoth(undefined, true)
    expect(screen.queryByText(/truncated/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/not shown here/i)).not.toBeInTheDocument()
  })

  it("annotates a surviving prefix with a chip rather than replacing it", () => {
    renderBoth("size", true)
    expect(screen.getByText(/truncated at 1 MiB/i)).toBeInTheDocument()
    // No standalone notice: the body is still on screen to read.
    expect(screen.queryByText(/not shown here/i)).not.toBeInTheDocument()
  })

  it("stands in for a hop body the per-trace budget dropped", () => {
    // The CDK deploy case. The reader must learn that the body is gone *and*
    // that the timing and status they are looking at are still real.
    renderBoth("trace-budget", false)
    expect(screen.getByText(/not shown here/i)).toBeInTheDocument()
    expect(screen.getByText(/8 MiB/)).toBeInTheDocument()
    expect(screen.getByText(/still recorded/i)).toBeInTheDocument()
    expect(screen.queryByRole("link")).not.toBeInTheDocument()
  })

  // Needs a real router: the link is a RouterLink, and asserting its href is
  // the point of the test.
  it("links to the hop's own trace, where the dropped body still lives", async () => {
    renderWithRouter(() => (
      <BodyOmissionNotice reason="trace-budget" hasBody={false} ownRequestId="req-42" />
    ))
    expect(await screen.findByRole("link", { name: /view it/i })).toHaveAttribute(
      "href",
      "/debug/traces/req-42",
    )
    // The sentence is split across the label span, the text node and the
    // link, so it is asserted on the rendered text rather than one element.
    expect(document.body.textContent).toMatch(/that trace holds the body in full/i)
  })

  it("says a streamed body was never held, not that it was dropped", () => {
    renderBoth("streaming", false)
    expect(screen.getByText(/streamed/i)).toBeInTheDocument()
    expect(screen.queryByText(/8 MiB/)).not.toBeInTheDocument()
  })
})
