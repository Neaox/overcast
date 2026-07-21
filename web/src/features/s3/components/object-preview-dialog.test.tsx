import { formatPreviewText } from "./object-preview-format"

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

  it("detects JSON content types with parameters", () => {
    const formatted = formatPreviewText('{"ok":true}', "application/json; charset=utf-8", "data")

    expect(formatted.text).toBe(["{", '  "ok": true', "}"].join("\n"))
    expect(formatted.html).toContain("token")
  })
})
