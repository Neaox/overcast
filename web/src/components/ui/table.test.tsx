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
    expect(head).toHaveClass("text-[9px]", "tracking-[0.14em]", "uppercase")
    expect(head).not.toHaveClass("tracking-[0.16em]")
  })
})
