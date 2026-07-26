import { useState } from "react"
import { render, screen } from "@/test/render"
import { Tab, TabList, Tabs } from "./tabs"

function Harness({ onSelect }: { onSelect?: (key: string) => void }) {
  const [selected, setSelected] = useState("overview")
  return (
    <Tabs
      selectedKey={selected}
      onSelectionChange={(key) => {
        setSelected(key)
        onSelect?.(key)
      }}
    >
      <TabList>
        <Tab id="overview">Overview</Tab>
        <Tab id="metrics">Metrics</Tab>
        <Tab id="triggers" isDisabled>
          Triggers
        </Tab>
      </TabList>
    </Tabs>
  )
}

describe("Tabs > keyboard navigation", () => {
  it("moves to the next tab on ArrowRight, which roving tabindex makes the only way there", async () => {
    const { user } = render(<Harness />)

    screen.getByRole("tab", { name: "Overview" }).focus()
    await user.keyboard("{ArrowRight}")

    expect(screen.getByRole("tab", { name: "Metrics" })).toHaveAttribute("aria-selected", "true")
  })

  it("wraps back to the last enabled tab on ArrowLeft from the first", async () => {
    const { user } = render(<Harness />)

    screen.getByRole("tab", { name: "Overview" }).focus()
    await user.keyboard("{ArrowLeft}")

    expect(screen.getByRole("tab", { name: "Metrics" })).toHaveAttribute("aria-selected", "true")
  })

  it("jumps to the first tab on Home", async () => {
    const { user } = render(<Harness />)

    screen.getByRole("tab", { name: "Overview" }).focus()
    await user.keyboard("{ArrowRight}{Home}")

    expect(screen.getByRole("tab", { name: "Overview" })).toHaveAttribute("aria-selected", "true")
  })

  it("skips a disabled tab", async () => {
    const onSelect = vi.fn()
    const { user } = render(<Harness onSelect={onSelect} />)

    screen.getByRole("tab", { name: "Overview" }).focus()
    await user.keyboard("{End}")

    expect(onSelect).not.toHaveBeenCalledWith("triggers")
  })
})

describe("Tabs > disabled tab", () => {
  it("does not select when clicked", async () => {
    const onSelect = vi.fn()
    const { user } = render(<Harness onSelect={onSelect} />)

    await user.click(screen.getByRole("tab", { name: "Triggers" }))

    expect(onSelect).not.toHaveBeenCalled()
  })

  it("shows the unavailable cursor rather than removing itself from hit testing", () => {
    render(<Harness />)

    expect(screen.getByRole("tab", { name: "Triggers" }).className).toContain(
      "disabled:cursor-not-allowed",
    )
  })
})
