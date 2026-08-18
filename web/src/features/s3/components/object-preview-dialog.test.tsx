import { render, screen } from "@/test/render"
import { ObjectPreviewDialog } from "./object-preview-dialog"
import { formatPreviewText, isTextPreviewable } from "./object-preview-format"

describe("formatPreviewText", () => {
  it("keeps self-closing XML tags at the current indentation level", () => {
    const formatted = formatPreviewText(
      "<root><empty/><with-space /><parent><child /></parent><after /></root>",
      "application/xml; charset=utf-8",
      "document.xml",
    )

    expect(formatted.text).toBe(
      [
        "<root>",
        "  <empty/>",
        "  <with-space />",
        "  <parent>",
        "    <child />",
        "  </parent>",
        "  <after />",
        "</root>",
      ].join("\n"),
    )
  })

  it("indents XML closing tags to match their opening tags", () => {
    const formatted = formatPreviewText(
      "<rss><channel><title>News</title><item><title>One</title><link>https://example.test/one</link></item><item><title>Two</title></item></channel></rss>",
      "application/rss+xml",
      "feed",
    )

    expect(formatted.text).toBe(
      [
        "<rss>",
        "  <channel>",
        "    <title>",
        "      News",
        "    </title>",
        "    <item>",
        "      <title>",
        "        One",
        "      </title>",
        "      <link>",
        "        https://example.test/one",
        "      </link>",
        "    </item>",
        "    <item>",
        "      <title>",
        "        Two",
        "      </title>",
        "    </item>",
        "  </channel>",
        "</rss>",
      ].join("\n"),
    )
    expect(formatted.html).toContain("token")
  })

  it("uses the object extension when the content type is generic", () => {
    const formatted = formatPreviewText(
      "<root><child>value</child></root>",
      "application/octet-stream",
      "backup.xml",
    )

    expect(isTextPreviewable("application/octet-stream", "backup.xml")).toBe(true)
    expect(formatted.text).toBe(
      ["<root>", "  <child>", "    value", "  </child>", "</root>"].join("\n"),
    )
    expect(formatted.html).toContain("token")
  })

  it("prefers a specific content type over a conflicting file extension", () => {
    const formatted = formatPreviewText(
      "<rss><channel><title>News</title></channel></rss>",
      "application/rss+xml",
      "feed.json",
    )

    expect(formatted.text).toBe(
      [
        "<rss>",
        "  <channel>",
        "    <title>",
        "      News",
        "    </title>",
        "  </channel>",
        "</rss>",
      ].join("\n"),
    )
  })

  it("detects JSON content types with parameters", () => {
    const formatted = formatPreviewText('{"ok":true}', "application/json; charset=utf-8", "data")

    expect(formatted.text).toBe(["{", '  "ok": true', "}"].join("\n"))
    expect(formatted.html).toContain("token")
  })

  it("highlights CSS and JavaScript", () => {
    expect(formatPreviewText("body { color: red }", "text/css", "site.css").html).toContain(
      "token selector",
    )
    expect(formatPreviewText("const x = 1", "text/javascript", "app.js").html).toContain(
      "token keyword",
    )
  })

  it("returns the identical highlight for a repeated document", () => {
    // The output feeds dangerouslySetInnerHTML; an identical string means
    // React leaves the DOM untouched on a re-render of the same preview.
    const text = "body { color: red }"
    expect(formatPreviewText(text, "text/css", "site.css").html).toBe(
      formatPreviewText(text, "text/css", "site.css").html,
    )
  })

  it("declines to highlight a script too large for the frame budget", () => {
    // Prism's cost is linear in the text (the logs work measured ~19 ms at
    // 166 KiB); a preview can hold up to 1 MiB, which would be a multi-hundred
    // millisecond freeze at dialog-open. Past the cap the preview is plain
    // text, and says so.
    const huge = `var x = 1;\n`.repeat(30_000) // ~330 KiB
    const formatted = formatPreviewText(huge, "text/javascript", "bundle.js")

    expect(formatted.html).toBeUndefined()
    expect(formatted.text).toBe(huge)
    expect(formatted.highlightSkipped).toBe(true)
  })

  it("says when a JSON document was too large to pretty-print", () => {
    const huge = JSON.stringify({ blob: "x".repeat(300_000) })
    const formatted = formatPreviewText(huge, "application/json", "blob.json")

    expect(formatted.html).toBeUndefined()
    expect(formatted.highlightSkipped).toBe(true)
  })

  it("does not blame size when JSON simply failed to parse", () => {
    const formatted = formatPreviewText('{"truncated":', "application/json", "part.json")

    expect(formatted.html).toBeUndefined()
    expect(formatted.highlightSkipped).toBeUndefined()
  })
})

// ─── The dialog ────────────────────────────────────────────────────────────
//
// `application/octet-stream` on a `.bin` key is deliberately not previewable,
// so these render without the body fetch a previewable object would make. What
// is under test is which *address* the dialog builds, not what it renders from
// the bytes.

const metadata = {
  contentType: "application/octet-stream",
  contentLength: 12,
  lastModified: "2026-08-10T00:00:00.000Z",
  etag: "d41d8cd98f00b204e9800998ecf8427e",
  metadata: {},
  storageClass: "STANDARD",
}

function downloadHref(): string {
  return screen.getByRole("link", { name: /download/i }).getAttribute("href") ?? ""
}

describe("ObjectPreviewDialog > a named version", () => {
  const renderVersion = (versionId: string) =>
    render(
      <ObjectPreviewDialog
        bucket="my-bucket"
        objectKey="report.bin"
        versionId={versionId}
        metadata={metadata}
        loading={false}
        onClose={() => {}}
      />,
    )

  it("downloads the version that was inspected, not the current one", () => {
    renderVersion("v2")
    expect(new URL(downloadHref(), "http://console.test").searchParams.get("versionId")).toBe("v2")
  })

  it('downloads by the literal id "null" rather than dropping it as falsy', () => {
    renderVersion("null")
    expect(new URL(downloadHref(), "http://console.test").searchParams.get("versionId")).toBe(
      "null",
    )
  })

  it("shows which version the metadata describes", () => {
    renderVersion("v2")
    expect(screen.getByText("Version")).toBeInTheDocument()
    expect(screen.getByText("v2")).toBeInTheDocument()
  })
})

describe("ObjectPreviewDialog > the current version", () => {
  beforeEach(() => {
    render(
      <ObjectPreviewDialog
        bucket="my-bucket"
        objectKey="report.bin"
        metadata={metadata}
        loading={false}
        onClose={() => {}}
      />,
    )
  })

  it("downloads without naming a version", () => {
    expect(new URL(downloadHref(), "http://console.test").searchParams.has("versionId")).toBe(false)
  })

  it("shows no version row, there being no version to name", () => {
    expect(screen.queryByText("Version")).not.toBeInTheDocument()
  })
})

describe("ObjectPreviewDialog > a version that cannot be read", () => {
  const renderError = (status: number, versionId?: string) =>
    render(
      <ObjectPreviewDialog
        bucket="my-bucket"
        objectKey="report.bin"
        versionId={versionId}
        metadata={undefined}
        loading={false}
        error={Object.assign(new Error("UnknownError"), {
          $metadata: { httpStatusCode: status },
        })}
        onClose={() => {}}
      />,
    )

  it("explains a delete marker instead of showing an empty dialog", () => {
    renderError(405, "v3")
    expect(screen.getByRole("alert")).toHaveTextContent(/delete marker/i)
  })

  it("names the version that no longer exists", () => {
    renderError(404, "v2")
    expect(screen.getByRole("alert")).toHaveTextContent("Version v2")
  })

  it("offers no download, there being nothing at that address to fetch", () => {
    renderError(404, "v2")
    expect(screen.queryByRole("link", { name: /download/i })).not.toBeInTheDocument()
  })
})
