import { describe, expect, it, vi } from "vitest"
import { render, screen } from "@/test/render"
import { RegionSelectCompact } from "./region-select"

/**
 * The compact region control is drawn as a split control — value cell, divider,
 * chevron cell — but it is still a combobox underneath. The chevron cell is
 * decoration, so it must open the list without ever taking focus off the input
 * that does the filtering.
 */
describe("RegionSelectCompact", () => {
  function setup() {
    const onChange = vi.fn()
    const view = render(<RegionSelectCompact value="us-east-1" onChange={onChange} />)
    const input = screen.getByRole("combobox")
    // aria-hidden by design — there is no accessible name to query it by.
    const chevronCell = input.parentElement!.lastElementChild!
    return { ...view, onChange, input, chevronCell }
  }

  it("opens the region list when the chevron cell is clicked", async () => {
    const { user, chevronCell } = setup()

    await user.click(chevronCell)

    expect(screen.getByRole("listbox")).toBeInTheDocument()
  })

  it("leaves focus on the filter input when the chevron cell is clicked", async () => {
    const { user, chevronCell, input } = setup()

    await user.click(chevronCell)

    expect(input).toHaveFocus()
  })

  it("filters the region list as the user types", async () => {
    const { user, input } = setup()

    await user.click(input)
    await user.type(input, "sydney")

    expect(screen.getByRole("option", { name: /ap-southeast-2/ })).toBeInTheDocument()
    expect(screen.queryByRole("option", { name: /us-east-1/ })).not.toBeInTheDocument()
  })

  it("selects the highlighted region on Enter", async () => {
    const { user, input, onChange } = setup()

    await user.click(input)
    await user.type(input, "sydney{Enter}")

    expect(onChange).toHaveBeenCalledWith("ap-southeast-2")
  })

  it("offers an unlisted region through the custom footer", async () => {
    const { user, input, onChange } = setup()

    await user.click(input)
    await user.type(input, "xx-nowhere-1")
    await user.click(screen.getByRole("button", { name: /Use custom region/ }))

    expect(onChange).toHaveBeenCalledWith("xx-nowhere-1")
  })
})
