import { readFileSync } from "node:fs"
import { describe, expect, it, vi } from "vitest"
import { createTestQueryClient, render, renderWithRouter, screen, within } from "@/test/render"
import { serverInfoQueryOptions } from "@/hooks/use-server-info"
import { EventConsole } from "./event-console"
import { defaultEventSummary } from "./event-summary"
import type { StreamEvent } from "@/types"

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

  describe("linking an event to its trace", () => {
    const REQUEST_ID = "3f2a1c88-0000-4444-8888-abcdefabcdef"

    const caused: StreamEvent = {
      type: "s3:ObjectCreated:*",
      source: "s3",
      time: "2026-07-14T12:00:00Z",
      requestId: REQUEST_ID,
      payload: { bucket: "assets-bucket", key: "logo.svg" },
    }

    const background: StreamEvent = {
      type: "docker:ContainerDied",
      source: "docker",
      time: "2026-07-14T12:00:01Z",
      payload: { containerId: "abc123" },
    }

    /**
     * Mounted at the trace route the link targets: TanStack Router resolves a
     * `to` against the real route tree, and a single-route memory tree has
     * nowhere for `/debug/traces/$requestId` to land.
     */
    function renderConsole(events: StreamEvent[], debug: boolean) {
      const queryClient = createTestQueryClient()
      queryClient.setQueryData(serverInfoQueryOptions().queryKey, { debug })
      return renderWithRouter(() => <EventConsole connected onClear={() => {}} events={events} />, {
        queryClient,
        path: "/debug/traces/$requestId",
        initialEntry: `/debug/traces/${REQUEST_ID}`,
      })
    }

    const traceLink = () => screen.queryByRole("link", { name: /Open trace for request/ })

    it("links a row to the trace of the request that caused it", async () => {
      renderConsole([caused], true)

      const link = await screen.findByRole("link", { name: `Open trace for request ${REQUEST_ID}` })
      expect(link.getAttribute("href")).toBe(`/debug/traces/${REQUEST_ID}`)
    })

    it("offers no link when the server is not recording traces", async () => {
      renderConsole([caused], false)

      // Wait for the row itself, so an absent link is a real absence rather
      // than an assertion that ran before the router's first commit.
      expect(await screen.findByText(/logo\.svg/)).toBeInTheDocument()
      expect(traceLink()).not.toBeInTheDocument()
    })

    it("offers no link for an event no request caused", async () => {
      renderConsole([background], true)

      expect(await screen.findByText("docker")).toBeInTheDocument()
      expect(traceLink()).not.toBeInTheDocument()
    })

    it("carries the request id in the copied envelope even with debug off", async () => {
      // The id belongs to the event, not to the link. On a server recording
      // no traces there is nothing to link to, but the id is still what a
      // reader pastes into the search box or hands to someone else.
      const { user } = renderConsole([caused], false)
      await screen.findByText(/logo\.svg/)

      const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined)
      Object.defineProperty(globalThis.navigator, "clipboard", {
        value: { writeText },
        configurable: true,
      })
      await user.click(screen.getByRole("button", { name: "Copy event" }))

      expect(writeText).toHaveBeenCalledWith(
        JSON.stringify(
          {
            type: "s3:ObjectCreated:*",
            time: "2026-07-14T12:00:00Z",
            source: "s3",
            requestId: REQUEST_ID,
            payload: { bucket: "assets-bucket", key: "logo.svg" },
          },
          null,
          2,
        ),
      )
    })
  })
})

/**
 * The console used to paint itself on a hardcoded `bg-[#0d0d0d]` with pale text
 * picked to sit on that slab, which made it a black rectangle in an otherwise
 * light page. Every colour it uses now has to resolve through a token, so the
 * regression is a test failure rather than a screenshot someone notices later.
 */
describe("EventConsole > theming", () => {
  const source = readFileSync("src/components/ui/event-console.tsx", "utf8")

  function renderOne(eventSource: string) {
    render(
      <EventConsole
        connected
        onClear={() => {}}
        events={[
          {
            type: `${eventSource}:Something`,
            source: eventSource,
            time: "2026-07-14T12:00:00Z",
            payload: {},
          },
        ]}
      />,
    )
  }

  it("paints the console surface with a theme token", () => {
    expect(source).toContain("bg-bg-elevated")
  })

  it("declares no literal colour value anywhere in the component", () => {
    // Tailwind arbitrary values: bg-[#0d0d0d], text-[rgb(…)], border-[hsl(…)]
    expect(source).not.toMatch(/-\[(?:#|rgba?\(|hsla?\(|oklch\()/)
  })

  it("declares no raw Tailwind palette hue anywhere in the component", () => {
    const RAW_HUE =
      /(?<![\w-])(?:bg|text|border|ring|fill|stroke|from|via|to)-(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|white|black)\b/g
    expect([...source.matchAll(RAW_HUE)].map((m) => m[0])).toEqual([])
  })

  it("colours a known source with a categorical ramp slot", () => {
    renderOne("lambda")
    expect(screen.getByText("lambda")).toHaveClass("text-cat-8")
  })

  it("gives two services that share a hue family different ramp slots", () => {
    // ecr and eventbridge were both text-rose-400 — indistinguishable.
    renderOne("ecr")
    renderOne("eventbridge")
    expect(screen.getByText("ecr").className).not.toEqual(screen.getByText("eventbridge").className)
  })

  it("falls back to a muted token for an unmapped source", () => {
    renderOne("some-unknown-service")
    expect(screen.getByText("some-unknown-service")).toHaveClass("text-fg-muted")
  })

  it("renders JSON strings through the shared Prism token class", async () => {
    const { user } = render(
      <EventConsole
        connected
        onClear={() => {}}
        events={[
          {
            type: "s3:BucketCreated",
            source: "s3",
            time: "2026-07-14T12:00:00Z",
            payload: { name: "assets-bucket" },
          },
        ]}
      />,
    )

    await user.click(screen.getByText("BucketCreated"))

    expect(screen.getByText('"assets-bucket"')).toHaveClass("token", "string")
  })

  describe("copying an event", () => {
    const event = {
      type: "s3:BucketCreated",
      source: "s3",
      time: "2026-07-14T12:00:00Z",
      resourceArn: "arn:aws:s3:::assets-bucket",
      payload: { name: "assets-bucket" },
    }

    /**
     * Installs a clipboard *after* render — `userEvent.setup()` puts its own
     * stub on `navigator.clipboard`, so an earlier one would be overwritten.
     */
    function renderWithClipboard() {
      const result = render(<EventConsole connected onClear={() => {}} events={[event]} />)
      const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined)
      Object.defineProperty(globalThis.navigator, "clipboard", {
        value: { writeText },
        configurable: true,
      })
      return { ...result, writeText }
    }

    it("offers a copy control on the row", () => {
      renderWithClipboard()
      expect(screen.getByRole("button", { name: "Copy event" })).toBeInTheDocument()
    })

    it("copies the whole envelope, not just the payload", async () => {
      const { user, writeText } = renderWithClipboard()

      await user.click(screen.getByRole("button", { name: "Copy event" }))

      expect(writeText).toHaveBeenCalledWith(
        JSON.stringify(
          {
            type: "s3:BucketCreated",
            time: "2026-07-14T12:00:00Z",
            source: "s3",
            resourceArn: "arn:aws:s3:::assets-bucket",
            payload: { name: "assets-bucket" },
          },
          null,
          2,
        ),
      )
    })

    it("does not expand the row it sits on", async () => {
      const { user } = renderWithClipboard()

      await user.click(screen.getByRole("button", { name: "Copy event" }))

      expect(screen.queryByText('"assets-bucket"')).not.toBeInTheDocument()
    })

    it("confirms the copy", async () => {
      const { user } = renderWithClipboard()

      await user.click(screen.getByRole("button", { name: "Copy event" }))

      expect(await screen.findByText("Copied event")).toBeInTheDocument()
    })
  })
})
