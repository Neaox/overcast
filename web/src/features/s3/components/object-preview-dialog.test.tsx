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
})
