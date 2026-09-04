import { render, screen } from "@/test/render"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableHead,
  TableHeader,
  TableRow,
} from "./table"

function rowsWith(onClick?: () => void) {
  return (
    <Table>
      <TableBody>
        <TableRow onClick={onClick}>
          <TableCell>assets</TableCell>
        </TableRow>
      </TableBody>
    </Table>
  )
}

describe("TableRow > navigating row", () => {
  it("is reachable by keyboard, so the row is not a mouse-only control", () => {
    render(rowsWith(() => {}))

    expect(screen.getByRole("row")).toHaveAttribute("tabindex", "0")
  })

  it("activates on Enter", async () => {
    const onClick = vi.fn()
    const { user } = render(rowsWith(onClick))

    screen.getByRole("row").focus()
    await user.keyboard("{Enter}")

    expect(onClick).toHaveBeenCalledOnce()
  })

  it("activates on Space", async () => {
    const onClick = vi.fn()
    const { user } = render(rowsWith(onClick))

    screen.getByRole("row").focus()
    await user.keyboard(" ")

    expect(onClick).toHaveBeenCalledOnce()
  })
})

describe("TableRow > inert row", () => {
  it("stays out of the tab order when it has nothing to activate", () => {
    render(rowsWith())

    expect(screen.getByRole("row")).not.toHaveAttribute("tabindex")
  })

  it("carries no focus treatment, so an unclickable row cannot look focusable", () => {
    render(rowsWith())

    expect(screen.getByRole("row")).not.toHaveClass("oc-row-focus")
  })
})

/*
 * The focus indicator (#1610). An `outline` on a <tr> inside a collapsed-border
 * table paints along the row's top and bottom and nothing down its sides, so a
 * focused row read as a pair of hairlines beside the four-sided ring every
 * other control gets. `oc-row-focus` (global.css) draws the ring across the
 * row's own cells instead — jsdom applies no stylesheet, so what
 * is assertable here is that the row opts in and does not also ask for the
 * outline the class exists to replace.
 */
describe("TableRow > focus indicator", () => {
  it("opts a navigating row into the cell-drawn focus ring", () => {
    render(rowsWith(() => {}))

    expect(screen.getByRole("row")).toHaveClass("oc-row-focus")
  })

  it("does not fall back to the outline that a collapsed-border row cannot paint", () => {
    render(rowsWith(() => {}))

    expect(screen.getByRole("row").className).not.toContain("outline-offset")
  })
})

describe("Table > narrow-width contract", () => {
  it("puts the table on a focusable scroller, so sideways columns are reachable without a trackpad", () => {
    render(rowsWith())

    // The scroller is the table's parent: it scrolls, so it has to be focusable
    // (WCAG 2.1.1) — see ScrollX.
    const scroller = screen.getByRole("table").parentElement
    expect(scroller).toHaveAttribute("tabindex", "0")
    expect(scroller).toHaveClass("overflow-auto")
  })
})

/* A table body is machine output, so mono is inherited rather than repeated at
   ~460 call sites. These two tests are the pair that keeps that safe: the
   default cannot quietly revert to sans, and the prose escape hatch cannot
   quietly be swallowed by it. */
describe("TableCell > typesetting", () => {
  it("sets body cells in mono without the call site asking for it", () => {
    render(
      <Table>
        <TableBody>
          <TableRow>
            <TableCell>arn:aws:sqs:ap-southeast-2:000000000000:orders</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )

    expect(screen.getByRole("cell")).toHaveClass("font-mono")
  })

  it("keeps a prose cell in sans, so a description does not read as an identifier", () => {
    render(
      <Table>
        <TableBody>
          <TableRow>
            <TableCellProse>Distributes the marketing site from the origin bucket.</TableCellProse>
          </TableRow>
        </TableBody>
      </Table>,
    )

    const cell = screen.getByRole("cell")
    expect(cell).toHaveClass("font-sans")
    expect(cell).not.toHaveClass("font-mono")
  })
})

describe("TableHead > typesetting", () => {
  it("uses the field-label tracking, not the wider section-heading tracking", () => {
    render(
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>queue name</TableHead>
          </TableRow>
        </TableHeader>
      </Table>,
    )

    const head = screen.getByRole("columnheader")
    expect(head).toHaveClass("text-2xs", "tracking-[0.14em]", "uppercase")
    expect(head).not.toHaveClass("tracking-[0.16em]")
  })
})
