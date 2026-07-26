import { render, screen } from "@/test/render"
import { Table, TableBody, TableCell, TableRow } from "./table"

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
