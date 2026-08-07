import { describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import userEvent from "@testing-library/user-event"
import { Combobox } from "./combobox"

const TWO_ITEMS = [
  { value: "http://localhost:" },
  { value: "http://localhost.overcast.sh:" },
]

function renderCombobox(props: Partial<Parameters<typeof Combobox>[0]> = {}) {
  const result = { value: "" }
  const { rerender } = render(
    <Combobox<{ value: string }>
      value={result.value}
      onChange={(v) => { result.value = v; rerender(<TestCombobox value={v} />) }}
      items={TWO_ITEMS}
      filterFn={(item, q) => item.value.includes(q)}
      getItemValue={(item) => item.value}
      renderItem={(item) => <span>{item.value}</span>}
      placeholder="..."
      {...props}
    />,
  )
  return result
}

function TestCombobox({ value }: { value: string }) {
  return (
    <Combobox<{ value: string }>
      value={value}
      onChange={() => {}}
      items={TWO_ITEMS}
      filterFn={(item, q) => item.value.includes(q)}
      getItemValue={(item) => item.value}
      renderItem={(item) => <span>{item.value}</span>}
      placeholder="..."
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
    const { user } = render(
      <Combobox<{ value: string }>
        value=""
        onChange={() => {}}
        items={TWO_ITEMS}
        filterFn={(item, q) => item.value.includes(q)}
        getItemValue={(item) => item.value}
        renderItem={(item) => <span>{item.value}</span>}
        placeholder="..."
        allowCustom
      />,
    )
    await user.click(screen.getByRole("combobox"))
    const items = screen.getAllByRole("option")
    expect(items).toHaveLength(2)
    await user.click(items[0])
    // Should not throw
  })

  it("selects second item without crashing", async () => {
    const { user } = render(
      <Combobox<{ value: string }>
        value=""
        onChange={() => {}}
        items={TWO_ITEMS}
        filterFn={(item, q) => item.value.includes(q)}
        getItemValue={(item) => item.value}
        renderItem={(item) => <span>{item.value}</span>}
        placeholder="..."
        allowCustom
      />,
    )
    await user.click(screen.getByRole("combobox"))
    const items = screen.getAllByRole("option")
    expect(items).toHaveLength(2)
    await user.click(items[1])
    // Should not throw
  })

  describe("allowFreeText", () => {
    it("seeds query from current value on re-open", async () => {
      const { user } = render(
        <Combobox<{ value: string }>
          value="http://localhost:"
          onChange={() => {}}
          items={TWO_ITEMS}
          filterFn={(item, q) => item.value.includes(q)}
          getItemValue={(item) => item.value}
          renderItem={(item) => <span>{item.value}</span>}
          placeholder="..."
          allowCustom
          allowFreeText
        />,
      )
      // Focus to open
      await user.click(screen.getByRole("combobox"))
      // The combobox input should show the current value as query
      expect(screen.getByRole("combobox")).toHaveValue("http://localhost:")
    })

    it("selects second item with allowFreeText without crashing", async () => {
      const { user } = render(
        <Combobox<{ value: string }>
          value=""
          onChange={() => {}}
          items={TWO_ITEMS}
          filterFn={(item, q) => item.value.includes(q)}
          getItemValue={(item) => item.value}
          renderItem={(item) => <span>{item.value}</span>}
          placeholder="..."
          allowCustom
          allowFreeText
        />,
      )
      await user.click(screen.getByRole("combobox"))
      const items = screen.getAllByRole("option")
      await user.click(items[1])
      // Should not throw
    })

    it("selects second item then re-opens without crashing", async () => {
      let value = ""
      const { user, rerender } = render(
        <Combobox<{ value: string }>
          value={value}
          onChange={(v) => { value = v }}
          items={TWO_ITEMS}
          filterFn={(item, q) => item.value.includes(q)}
          getItemValue={(item) => item.value}
          renderItem={(item) => <span>{item.value}</span>}
          placeholder="..."
          allowCustom
          allowFreeText
        />,
      )
      // Select second item
      await user.click(screen.getByRole("combobox"))
      await user.click(screen.getAllByRole("option")[1])

      // Re-render with new value
      rerender(
        <Combobox<{ value: string }>
          value={value}
          onChange={(v) => { value = v }}
          items={TWO_ITEMS}
          filterFn={(item, q) => item.value.includes(q)}
          getItemValue={(item) => item.value}
          renderItem={(item) => <span>{item.value}</span>}
          placeholder="..."
          allowCustom
          allowFreeText
        />,
      )

      // Re-open
      await user.click(screen.getByRole("combobox"))
      // Should show the selected value as query
      expect(screen.getByRole("combobox")).toHaveValue(value)
    })
  })
})
