import { describe, expect, it, vi } from "vitest"
import { render, screen, within } from "@/test/render"
import { EventConsole } from "./event-console"
import { defaultEventSummary } from "./event-summary"

vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: ({ count }: { count: number }) => ({
    getTotalSize: () => count * 34,
    getVirtualItems: () =>
      Array.from({ length: count }, (_, index) => ({
        index,
        key: index,
        start: index * 34,
      })),
    measureElement: vi.fn(),
    scrollToIndex: vi.fn(),
  }),
}))

describe("EventConsole", () => {
  it("summarizes S3 bucket lifecycle events from resource payload names", () => {
    expect(
      defaultEventSummary({
        type: "s3:BucketCreated",
        source: "s3",
        time: "2026-07-14T12:00:00Z",
        payload: { name: "assets-bucket" },
      }),
    ).toBe("s3://assets-bucket")

    expect(
      defaultEventSummary({
        type: "s3:BucketDeleted",
        source: "s3",
        time: "2026-07-14T12:00:00Z",
        payload: { Name: "old-assets-bucket" },
      }),
    ).toBe("s3://old-assets-bucket")
  })

  it("summarizes S3 object events from notification payload bucket and key", () => {
    const summary = defaultEventSummary({
      type: "s3:ObjectCreated:*",
      source: "s3",
      time: "2026-07-14T12:00:00Z",
      payload: { Bucket: "assets-bucket", Key: "images/logo.png", Size: 2048 },
    })

    expect(summary).toBe("s3://assets-bucket/images/logo.png (2.0 KB)")
  })

  it("shows decoded base64 JSON values by default in expanded payloads", async () => {
    const { user } = render(
      <EventConsole
        connected
        onClear={() => {}}
        events={[
          {
            type: "lambda:ESMInvoked",
            source: "lambda",
            time: "2026-07-14T12:00:00Z",
            payload: { data: "eyJtZXNzYWdlIjoiaGVsbG8iLCJjb3VudCI6Mn0=" },
          },
        ]}
      />,
    )

    await user.click(screen.getByText("ESMInvoked"))

    expect(screen.getByText("decoded JSON")).toBeInTheDocument()
    expect(screen.getByText(/"message"/)).toBeInTheDocument()
    expect(screen.getByText(/"hello"/)).toBeInTheDocument()
  })

  it("toggles decoded base64 values back to raw payload text", async () => {
    const encoded = "aGVsbG8td29ybGQ="
    const { user } = render(
      <EventConsole
        connected
        onClear={() => {}}
        events={[
          {
            type: "sqs:MessageSent",
            source: "sqs",
            time: "2026-07-14T12:00:00Z",
            payload: { body: encoded },
          },
        ]}
      />,
    )

    await user.click(screen.getByText("MessageSent"))

    expect(screen.getByText("decoded")).toBeInTheDocument()
    expect(screen.getByText("hello-world")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Show raw value at $.payload.body" }))

    expect(screen.getByText("raw")).toBeInTheDocument()
    expect(
      within(screen.getByText("raw").parentElement!).getByText(JSON.stringify(encoded)),
    ).toBeInTheDocument()
  })
})
