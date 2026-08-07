import { describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import userEvent from "@testing-library/user-event"
import { Combobox } from "./combobox"

const TWO_ITEMS = [
  { value: "http://localhost:" },
  { value: "http://localhost.overcast.sh:" },
]

function TestCombobox({ value, onChange, allowFreeText, allowCustom }: { value: string; onChange?: (v: string) => void; allowFreeText?: boolean; allowCustom?: boolean }) {
  return (
    <Combobox<{ value: string }>
      value={value}
      onChange={onChange ?? (() => {})}
      items={TWO_ITEMS}
      filterFn={(item, q) => item.value.includes(q)}
      getItemValue={(item) => item.value}
      renderItem={(item) => <span>{item.value}</span>}
      placeholder="..."
      allowFreeText={allowFreeText}
      allowCustom={allowCustom}
    />
  )
}

describe("Combobox", () => {
  it("opens dropdown on focus", async () => {
    render(<TestCombobox value="" />)
    await userEvent.click(screen.getByRole("combobox"))
    expect(screen.getByRole("listbox")).toBeInTheDocument()
  })

  it("selects first item without crashing", async () => {
    const { user } = render(<TestCombobox value="" />)
    await user.click(screen.getByRole("combobox"))
    const items = screen.getAllByRole("option")
    expect(items).toHaveLength(2)
    await user.click(items[0])
  })

  it("selects second item without crashing", async () => {
    const { user } = render(<TestCombobox value="" />)
    await user.click(screen.getByRole("combobox"))
    const items = screen.getAllByRole("option")
    expect(items).toHaveLength(2)
    await user.click(items[1])
  })

  describe("allowFreeText", () => {
    it("seeds query from current value on re-open", async () => {
      const { user } = render(<TestCombobox value="http://localhost:" allowFreeText />)
      await user.click(screen.getByRole("combobox"))
      expect(screen.getByRole("combobox")).toHaveValue("http://localhost:")
    })

    it("selects second item with allowFreeText without crashing", async () => {
      const { user } = render(<TestCombobox value="" allowFreeText allowCustom />)
      await user.click(screen.getByRole("combobox"))
      const items = screen.getAllByRole("option")
      await user.click(items[1])
    })

    it("selects item then re-opens without crashing", async () => {
      let value = ""
      const { user, rerender } = render(
        <TestCombobox
          value={value}
          onChange={(v) => { value = v }}
          allowFreeText
          allowCustom
        />,
      )
      await user.click(screen.getByRole("combobox"))
      await user.click(screen.getAllByRole("option")[1])

      rerender(<TestCombobox value={value} onChange={(v) => { value = v }} allowFreeText allowCustom />)

      await user.click(screen.getByRole("combobox"))
      expect(screen.getByRole("combobox")).toHaveValue(value)
    })
  })
})
