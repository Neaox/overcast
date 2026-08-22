import { render, screen } from "@/test/render"
import { ResourceListSection } from "./resource-list-section"

describe("ResourceListSection", () => {
  it("renders the actions row above the children", () => {
    render(
      <ResourceListSection actions={<button>Refresh</button>}>
        <div>the table</div>
      </ResourceListSection>,
    )
    expect(screen.getByRole("button", { name: "Refresh" })).toBeInTheDocument()
    expect(screen.getByText("the table")).toBeInTheDocument()
  })

  it("omits the actions row entirely when actions is not passed", () => {
    const { container } = render(
      <ResourceListSection>
        <div>the table</div>
      </ResourceListSection>,
    )
    // Only one child div (the content) — no empty actions wrapper left behind.
    expect(container.firstElementChild?.children).toHaveLength(1)
  })

  it("renders extra content between the actions row and the table", () => {
    render(
      <ResourceListSection actions={<button>Refresh</button>}>
        <div>state filter chips</div>
        <div>the table</div>
      </ResourceListSection>,
    )
    const chips = screen.getByText("state filter chips")
    const table = screen.getByText("the table")
    // Order matters: chips render before the table, both after the actions row.
    expect(chips.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })
})
