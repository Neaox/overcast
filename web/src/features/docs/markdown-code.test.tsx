/**
 * The fence→language policy behind both markdown surfaces (the /docs route
 * and the service-docs modal), and the shared block renderer it feeds. jsdom
 * has no CSS Custom Highlight API, so rendering here exercises the markup
 * backend — token spans in the DOM.
 */
import { render } from "@testing-library/react"
import { MarkdownCodeBlock, fenceLanguage } from "./markdown-code"

describe("fenceLanguage", () => {
  it.each([
    // Canonical names and Prism-registered aliases (sh, ts, js, yml…).
    ["language-bash", "bash"],
    ["language-sh", "bash"],
    ["language-go", "go"],
    ["language-typescript", "typescript"],
    ["language-ts", "typescript"],
    ["language-js", "javascript"],
    ["language-json", "json"],
    ["language-yaml", "yaml"],
    ["language-yml", "yaml"],
    ["language-python", "python"],
    ["language-java", "java"],
    ["language-csharp", "csharp"],
    ["language-powershell", "powershell"],
    ["language-sql", "sql"],
    // Fence-only aliases: names Prism has no entry for.
    ["language-jsonc", "json"],
    ["language-tsx", "typescript"],
  ] as const)("resolves %s to the registered grammar %s", (className, language) => {
    expect(fenceLanguage(className)).toBe(language)
  })

  it.each([
    "language-mermaid",
    "language-text", // registered, but as the empty plain grammar
    "language-rust",
    "language-hcl",
  ])("maps %s to null — the render-plain policy", (className) => {
    expect(fenceLanguage(className)).toBeNull()
  })

  it("returns null without a language- class (inline code, bare fences)", () => {
    expect(fenceLanguage(undefined)).toBeNull()
    expect(fenceLanguage("")).toBeNull()
    expect(fenceLanguage("md-link")).toBeNull()
  })
})

describe("MarkdownCodeBlock", () => {
  it("highlights a known-language fence and strips the fence's trailing newline", () => {
    const { container } = render(
      <MarkdownCodeBlock>
        <code className="language-go">{'if err != nil {\n\treturn ""\n}\n'}</code>
      </MarkdownCodeBlock>,
    )
    const pre = container.querySelector("pre")
    expect(pre?.textContent).toBe('if err != nil {\n\treturn ""\n}')
    expect(pre?.querySelectorAll("span.token").length).toBeGreaterThan(0)
  })

  it("renders an unknown-language fence plain, in the same styled pre", () => {
    const highlighted = render(
      <MarkdownCodeBlock>
        <code className="language-go">{"x := 1\n"}</code>
      </MarkdownCodeBlock>,
    )
    const plain = render(
      <MarkdownCodeBlock>
        <code className="language-mermaid">{"graph TD\n"}</code>
      </MarkdownCodeBlock>,
    )
    const pre = plain.container.querySelector("pre")
    expect(pre?.textContent).toBe("graph TD")
    expect(pre?.querySelectorAll("span.token")).toHaveLength(0)
    // Same pre, same classes, whatever the language resolved to.
    expect(pre?.className).toBe(highlighted.container.querySelector("pre")?.className)
  })
})
