import { readFileSync } from "node:fs"

describe("Prism token theme", () => {
  it("defines colours for HTML, XML, CSS, and JavaScript token classes", () => {
    const css = readFileSync("src/styles/global.css", "utf8")
    const requiredTokenSelectors = [
      ".token.tag",
      ".token.attr-name",
      ".token.attr-value",
      ".token.doctype",
      ".token.doctype-tag",
      ".token.name",
      ".token.selector",
      ".token.keyword",
      ".token.function",
      ".token.parameter",
      ".token.operator",
      ".token.template-string",
      ".token.interpolation",
    ]

    for (const selector of requiredTokenSelectors) {
      expect(css).toContain(selector)
    }
  })

  it("keeps explicit dark-mode number tokens orange", () => {
    const css = readFileSync("src/styles/global.css", "utf8")

    expect(css).toMatch(
      /\.dark \.token\.number,\s*\[data-theme="dark"\] \.token\.number \{\s*color: oklch\(0\.75 0\.14 55\);\s*\}/,
    )
    expect(css).not.toMatch(
      /\[data-theme="dark"\] \.token\.number,[^{]+\{\s*color: oklch\(0\.72 0\.15 290\);/,
    )
  })
})
