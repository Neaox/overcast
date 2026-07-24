import { http, HttpResponse } from "msw"
import { fireEvent, render, screen, waitFor, within } from "@/test/render"
import { server } from "@/test/server"
import { debugClipboard } from "./clipboard"
import { DebugPage } from "./debug-page"

/** Wraps a flat key->value map in the paginated /_debug/state/{ns} response shape. */
const namespacePage = (values: Record<string, string>) => HttpResponse.json({ values })

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
        namespacePage({
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
    expect((await screen.findAllByRole("button", { name: /queues/ })).length).toBeGreaterThan(0)
    expect(screen.getAllByText("sqs:queues").length).toBeGreaterThan(0)
    expect((await screen.findAllByText("us-east-1/my-dlq.fifo")).length).toBeGreaterThan(1)
    const viewer = screen.getByText(/MessageRetentionPeriod/).closest("section")
    expect(viewer).not.toBeNull()
    expect(within(viewer as HTMLElement).getByText('"MessageRetentionPeriod"')).toBeInTheDocument()
    expect(within(viewer as HTMLElement).getByText('"0"')).toBeInTheDocument()
  })

  it("shows slash-delimited keys as a tree", async () => {
    // Given: S3 object state contains CDK asset keys nested under a bucket name.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({
          "s3:objects": [
            "cdk-hnb659fds-assets-000000000000-us-east-1/0f677178c635e11d325450ded55a9c7dea158330a1e7cfdeba69cbf7bdfd96ac.json",
          ],
        }),
      ),
      http.get("/api/debug/state/s3%3Aobjects", () =>
        namespacePage({
          "cdk-hnb659fds-assets-000000000000-us-east-1/0f677178c635e11d325450ded55a9c7dea158330a1e7cfdeba69cbf7bdfd96ac.json":
            JSON.stringify({ body: "{}" }),
        }),
      ),
    )

    // When: the raw state debugger opens in its default tree view.
    render(<DebugPage />)

    // Then: the bucket-like prefix and object leaf are shown separately.
    expect(await screen.findByText("cdk-hnb659fds-assets-000000000000-us-east-1")).toBeInTheDocument()
    expect(
      screen.getByText("0f677178c635e11d325450ded55a9c7dea158330a1e7cfdeba69cbf7bdfd96ac.json"),
    ).toBeInTheDocument()
  })

  it("decodes nested JSON string values", async () => {
    // Given: a stored record contains an escaped JSON document inside a string field.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "appsync": ["us-east-1:resolver:api-id:Query:namespaces"] }),
      ),
      http.get("/api/debug/state/appsync", () =>
        namespacePage({
          "us-east-1:resolver:api-id:Query:namespaces": JSON.stringify({
            requestMappingTemplate: JSON.stringify({ version: "2018-05-29", operation: "Invoke" }),
          }),
        }),
      ),
    )

    // When: the raw value is rendered.
    render(<DebugPage />)

    // Then: the nested string is expanded into readable highlighted JSON.
    expect(await screen.findByText('"requestMappingTemplate"')).toBeInTheDocument()
    expect(screen.getByText('"operation"')).toBeInTheDocument()
    expect(screen.getByText('"Invoke"')).toBeInTheDocument()
  })

  it("renders backend-truncated large JSON string values", async () => {
    // Given: Lambda layer state contains a backend-truncated zip payload field.
    const largeLayer = JSON.stringify({
      layer_name: "deps",
      content: `${"A".repeat(1024)}...(truncated)`,
    })
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "lambda:layers": ["us-east-1/deps:0000000001"] }),
      ),
      http.get("/api/debug/state/lambda%3Alayers", () =>
        namespacePage({ "us-east-1/deps:0000000001": largeLayer }),
      ),
    )

    // When: the raw Lambda layer record is selected.
    render(<DebugPage />)

    // Then: metadata remains readable and the truncation marker is visible.
    expect(await screen.findByText(/"layer_name"/)).toBeInTheDocument()
    const viewer = screen.getByText(/"layer_name"/).closest("section")
    expect(viewer).not.toBeNull()
    expect(within(viewer as HTMLElement).getByText(/"layer_name"/)).toBeInTheDocument()
    expect(within(viewer as HTMLElement).getByText(/"content"/)).toBeInTheDocument()
    expect(within(viewer as HTMLElement).getByText(/\.\.\.\(truncated\)/)).toBeInTheDocument()
  })

  it("filters keys by raw stored values", async () => {
    // Given: a namespace contains multiple records.
    server.use(
      http.get("/api/debug/state", () =>
        HttpResponse.json({ "sqs:queues": ["us-east-1/orders", "us-east-1/orders-dlq.fifo"] }),
      ),
      http.get("/api/debug/state/sqs%3Aqueues", () =>
        namespacePage({
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
        namespacePage({
          "us-east-1/my-dlq.fifo": JSON.stringify({ name: "my-dlq.fifo" }),
        }),
      ),
    )

    // When: the user copies the raw value and direct debug link.
    render(<DebugPage />)
    await screen.findByText('"name"')
    await screen.findByText('"my-dlq.fifo"')
    const valueButton = screen.getByRole("button", { name: "Value" })
    await waitFor(() => expect(valueButton).toBeEnabled())
    fireEvent.click(valueButton)
    await waitFor(() => expect(clipboardWriteText).toHaveBeenCalled())
    expect(screen.getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/api/debug/state/sqs%3Aqueues?key=us-east-1%2Fmy-dlq.fifo",
    )
    expect(screen.getByRole("link", { name: "Open" })).toHaveAttribute("target", "_blank")
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
