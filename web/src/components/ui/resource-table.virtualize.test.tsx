import { render, screen, within } from "@/test/render"
import { ResourceTable } from "./resource-table"

/**
 * jsdom gives every element a zero height, so the real virtualizer renders no
 * rows at all and nothing about the table would be observable. The mock is the
 * same shape `log-search-results.test.tsx` uses, narrowed to a fixed window so
 * the assertions can be about *which* rows reach the DOM.
 */
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => {
    const windowed = Math.min(count, 3)
    return {
      getTotalSize: () => count * 37,
      getVirtualItems: () =>
        Array.from({ length: windowed }, (_, index) => ({
          index,
          key: index,
          start: index * 37,
          end: index * 37 + 37,
          size: 37,
        })),
      measureElement: vi.fn(),
      scrollToIndex: vi.fn(),
      scrollOffset: 0,
    }
  },
}))

interface Stream {
  name: string
}

const streams: Stream[] = Array.from({ length: 20 }, (_, i) => ({
  name: `stream-${String(i).padStart(2, "0")}`,
}))

function VirtualStreams(props: { virtualize?: boolean }) {
  return (
    <ResourceTable
      query={{ data: streams, isLoading: false }}
      noun="streams"
      rowKey={(s) => s.name}
      virtualize={props.virtualize}
      columns={[{ header: "Name", sortValue: (s) => s.name, cell: (s) => s.name }]}
    />
  )
}

function bodyRows() {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body).getAllByRole("row")
}

describe("ResourceTable > virtualize", () => {
  it("renders every row when virtualization is off", () => {
    render(<VirtualStreams />)
    expect(bodyRows()).toHaveLength(20)
    expect(screen.queryByTestId("virtual-scroll")).not.toBeInTheDocument()
  })

  it("renders only the virtual window, inside a scroll viewport", () => {
    const { container } = render(<VirtualStreams virtualize />)
    expect(container.querySelector('[data-slot="virtual-scroll"]')).toBeInTheDocument()

    const rows = bodyRows()
    expect(rows).toHaveLength(3)
    expect(within(rows[0]).getByText("stream-00")).toBeInTheDocument()
    expect(screen.queryByText("stream-19")).not.toBeInTheDocument()

    // A trailing spacer row reserves the rest of the scroll height. That is
    // the technique that keeps the real <table> laying out the columns rather
    // than absolutely positioning rows — the header stays aligned.
    const spacer = container.querySelector<HTMLElement>("tr[aria-hidden] td")
    expect(spacer?.style.height).toBe(`${(20 - 3) * 37}px`)
  })

  it("still sorts through the same engine when virtualized", async () => {
    const { user } = render(<VirtualStreams virtualize />)
    await user.click(screen.getByRole("button", { name: "Name" }))
    await user.click(screen.getByRole("button", { name: "Name" }))

    // Descending: the window now starts at the last name.
    expect(within(bodyRows()[0]).getByText("stream-19")).toBeInTheDocument()
  })
})
