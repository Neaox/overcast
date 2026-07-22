import { http, HttpResponse } from "msw"
import { fireEvent, render, screen, waitFor, within } from "@/test/render"
import { server } from "@/test/server"
import { debugClipboard } from "./clipboard"
import { DebugPage } from "./debug-page"

describe("DebugPage", () => {
  const clipboardWriteText = vi.fn<() => Promise<void>>()

  beforeEach(() => {
    clipboardWriteText.mockReset()
    clipboardWriteText.mockResolvedValue(undefined)
    vi.spyOn(debugClipboard, "writeText").mockImplementation(clipboardWriteText)
  })

  it("shows raw queue attributes from the state store", async () => {
    // Given: the debug API exposes an SQS queue record with an invalid retained value.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "sqs:queues": ["us-east-1/my-dlq.fifo"] }),
      ),
      http.get("/api/debug/state/sqs%3Aqueues", () =>
        HttpResponse.json({
          "us-east-1/my-dlq.fifo": JSON.stringify({
            name: "my-dlq.fifo",
            attributes: { MessageRetentionPeriod: "0", FifoQueue: "true" },
          }),
        }),
      ),
    )

    // When: the raw state debugger is opened.
    render(<DebugPage />)

    // Then: the namespace, key, and pretty-printed raw value are visible read-only.
    expect(await screen.findAllByRole("button", { name: /sqs:queues/ })).toHaveLength(2)
    expect((await screen.findAllByText("us-east-1/my-dlq.fifo")).length).toBeGreaterThan(1)
    const viewer = screen.getByText(/MessageRetentionPeriod/).closest("section")
    expect(viewer).not.toBeNull()
    expect(
      within(viewer as HTMLElement).getByText(/"MessageRetentionPeriod": "0"/),
    ).toBeInTheDocument()
  })

  it("filters keys by raw stored values", async () => {
    // Given: a namespace contains multiple records.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "sqs:queues": ["us-east-1/orders", "us-east-1/orders-dlq.fifo"] }),
      ),
      http.get("/api/debug/state/sqs%3Aqueues", () =>
        HttpResponse.json({
          "us-east-1/orders": JSON.stringify({
            name: "orders",
            attributes: { FifoQueue: "false" },
          }),
          "us-east-1/orders-dlq.fifo": JSON.stringify({
            name: "orders-dlq.fifo",
            attributes: { FifoQueue: "true" },
          }),
        }),
      ),
    )

    // When: the user searches across raw values.
    const { user } = render(<DebugPage />)
    await user.type(await screen.findByLabelText("Filter raw state keys and values"), "orders-dlq")

    // Then: only matching records remain in the key list.
    expect(screen.getAllByText("us-east-1/orders-dlq.fifo").length).toBeGreaterThan(0)
    expect(screen.queryByText("us-east-1/orders")).not.toBeInTheDocument()
    expect(screen.getByText(/Showing 1 of 2 records/)).toBeInTheDocument()
  })

  it("copies the selected raw value and deep link", async () => {
    // Given: the debug API exposes a selected SQS queue record.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "sqs:queues": ["us-east-1/my-dlq.fifo"] }),
      ),
      http.get("/api/debug/state/sqs%3Aqueues", () =>
        HttpResponse.json({
          "us-east-1/my-dlq.fifo": JSON.stringify({ name: "my-dlq.fifo" }),
        }),
      ),
    )

    // When: the user copies the raw value and direct debug link.
    render(<DebugPage />)
    await screen.findByText(/"name": "my-dlq.fifo"/)
    const valueButton = screen.getByRole("button", { name: "Value" })
    await waitFor(() => expect(valueButton).toBeEnabled())
    fireEvent.click(valueButton)
    await waitFor(() => expect(clipboardWriteText).toHaveBeenCalled())
    fireEvent.click(screen.getByRole("button", { name: "Link" }))

    // Then: the clipboard receives the raw value and a link scoped to the record.
    expect(clipboardWriteText).toHaveBeenCalledWith(JSON.stringify({ name: "my-dlq.fifo" }))
    expect(clipboardWriteText).toHaveBeenCalledWith(
      "http://localhost:3000/debug?namespace=sqs%3Aqueues&key=us-east-1%2Fmy-dlq.fifo",
    )
  })

  it("explains when debug mode is disabled", async () => {
    // Given: the BFF reports the emulator debug namespace is disabled.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json(
          {
            error: "DebugDisabled",
            message: "OVERCAST_DEBUG must be enabled to inspect raw state.",
          },
          { status: 404 },
        ),
      ),
    )

    // When: the raw state debugger is opened.
    render(<DebugPage />)

    // Then: the page surfaces the debug-mode requirement.
    expect(
      await screen.findByText("OVERCAST_DEBUG must be enabled to inspect raw state."),
    ).toBeInTheDocument()
  })
})
