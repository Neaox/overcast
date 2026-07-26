import { readFileSync } from "node:fs"
import { render, screen } from "@/test/render"
import { QueryListState, Spinner } from "@/components/ui/primitives"
import { SkeletonCards, SkeletonRows } from "@/components/ui/skeleton"

const rows = (container: HTMLElement) => container.querySelectorAll('[data-slot="skeleton-row"]')
const cards = (container: HTMLElement) => container.querySelectorAll('[data-slot="skeleton-card"]')

describe("QueryListState > loading", () => {
  it("renders skeleton rows instead of a centred spinner", () => {
    const { container } = render(<QueryListState isLoading isEmpty={false} />)

    expect(rows(container)).toHaveLength(5)
    expect(container.querySelector("svg.animate-spin")).toBeNull()
  })

  it("announces the loading state to assistive technology", () => {
    render(<QueryListState isLoading isEmpty={false} />)

    expect(screen.getByRole("status")).toBeInTheDocument()
  })

  it("honours the placeholder row count", () => {
    const { container } = render(<QueryListState isLoading isEmpty={false} loadingCount={3} />)

    expect(rows(container)).toHaveLength(3)
  })

  it("names the resource being loaded in the footer", () => {
    render(<QueryListState isLoading isEmpty={false} loadingNoun="log groups" />)

    expect(screen.getByText("loading log groups")).toBeInTheDocument()
  })

  it("renders card skeletons when the loading variant is cards", () => {
    const { container } = render(
      <QueryListState isLoading isEmpty={false} loadingVariant="cards" />,
    )

    expect(cards(container)).toHaveLength(3)
  })

  it("still renders the empty state once loading finishes", () => {
    render(<QueryListState isLoading={false} isEmpty emptyTitle="No log groups yet" />)

    expect(screen.getByText("No log groups yet")).toBeInTheDocument()
    expect(screen.queryByRole("status")).not.toBeInTheDocument()
  })
})

describe("SkeletonRows", () => {
  it("cycles the fixed bar widths when asked for more rows than the design array", () => {
    const { container } = render(<SkeletonRows rows={7} />)

    const widths = [...rows(container)].map((row) => row.firstElementChild?.className)
    expect(widths[5]).toBe(widths[0])
    expect(widths[6]).toBe(widths[1])
  })

  it("never applies a pulse or shimmer animation to a bar", () => {
    const { container } = render(<SkeletonRows />)

    expect(container.querySelector(".animate-pulse")).toBeNull()
    expect(container.querySelector('[class*="shimmer"]')).toBeNull()
  })

  it("renders exactly one blinking cursor, in the footer", () => {
    const { container } = render(<SkeletonRows noun="buckets" />)

    expect(container.querySelectorAll(".oc-cursor-blink")).toHaveLength(1)
  })
})

describe("SkeletonCards", () => {
  it("honours the placeholder card count", () => {
    const { container } = render(<SkeletonCards cards={6} />)

    expect(cards(container)).toHaveLength(6)
  })

  it("announces the resource being loaded without drawing footer copy", () => {
    render(<SkeletonCards noun="services" />)

    expect(screen.getByText("loading services")).toHaveClass("sr-only")
  })
})

describe("Spinner", () => {
  it("stays within the 14-16px ceiling when a caller asks for a larger size", () => {
    const { container } = render(<Spinner className="h-6 w-6" />)

    expect(container.firstElementChild).toHaveClass("h-4", "w-4")
    expect(container.firstElementChild).not.toHaveClass("h-6", "w-6")
  })

  it("keeps non-sizing utilities passed by the caller", () => {
    const { container } = render(<Spinner className="mr-2 text-accent" />)

    expect(container.firstElementChild).toHaveClass("mr-2", "text-accent")
  })
})

/* The blink keyframes live in global.css, so jsdom cannot evaluate them —
   window.matchMedia is stubbed to always report `matches: false`. Assert the
   contract at the source instead: under `reduce` the cursor must go solid, not
   disappear. Hiding it would reflow the sentence it sits inside. */
describe("prefers-reduced-motion", () => {
  const css = readFileSync("src/styles/global.css", "utf8")
  const reduceBlock = css.slice(css.indexOf("@media (prefers-reduced-motion: reduce)"))

  it("declares the cursor blink as a stepped animation", () => {
    expect(css).toMatch(
      /\.oc-cursor-blink\s*\{\s*animation:\s*oc-cursor-blink 1\.1s steps\(1, end\) infinite;/,
    )
  })

  it("renders the cursor solid rather than hidden under reduce", () => {
    expect(reduceBlock).toMatch(/\.oc-cursor-blink\s*\{[^}]*animation:\s*none;/)
    expect(reduceBlock).toMatch(/\.oc-cursor-blink\s*\{[^}]*opacity:\s*1;/)
    expect(reduceBlock).not.toMatch(
      /\.oc-cursor-blink\s*\{[^}]*(display:\s*none|visibility:\s*hidden)/,
    )
  })
})
