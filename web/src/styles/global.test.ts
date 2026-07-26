import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"

/** Colour utilities Tailwind resolves via `--color-*` in the `@theme` block. */
const COLOUR_UTILITY =
  /\b(?:bg|text|border)-(bg|fg|accent|danger|warning|success|border|sidebar|cloud)[a-z-]*\b/g

/** Names declared as `--color-<name>` inside global.css's `@theme { … }` block. */
function declaredThemeColours(css: string): Set<string> {
  const start = css.indexOf("@theme {")
  expect(start).toBeGreaterThanOrEqual(0)
  const block = css.slice(start, css.indexOf("}", start))
  return new Set([...block.matchAll(/--color-([a-z0-9-]+)\s*:/g)].map((m) => m[1]))
}

function sourceFiles(dir: string, acc: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) sourceFiles(path, acc)
    else if (/\.tsx?$/.test(path)) acc.push(path)
  }
  return acc
}

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

/* Tailwind silently emits nothing for a colour utility whose `--color-*` variable
   isn't declared, so a typo'd or invented token (bg-bg-card, text-fg-accent) fails
   open: the element renders transparent/inherited instead of erroring at build. */
describe("semantic colour tokens", () => {
  it("resolves every colour utility used in src/ to a --color-* declared in @theme", () => {
    const declared = declaredThemeColours(readFileSync("src/styles/global.css", "utf8"))
    const unresolved: string[] = []

    for (const file of sourceFiles("src")) {
      // this file quotes undeclared tokens on purpose, as counterexamples
      if (file.endsWith("global.test.ts")) continue
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          for (const match of line.matchAll(COLOUR_UTILITY)) {
            const utility = match[0]
            // strip the bg-/text-/border- prefix to get the theme token name
            const token = utility.slice(utility.indexOf("-") + 1)
            if (!declared.has(token)) {
              unresolved.push(`${file}:${i + 1}  ${utility}  (no --color-${token})`)
            }
          }
        })
    }

    expect(unresolved, `Undeclared colour tokens:\n${unresolved.join("\n")}`).toEqual([])
  })

  it("flags a utility whose token is absent from @theme", () => {
    const declared = declaredThemeColours(readFileSync("src/styles/global.css", "utf8"))

    // guard-the-guard: the exact bug this test exists to catch
    expect(declared.has("bg-card")).toBe(false)
    expect([..."bg-bg-card".matchAll(COLOUR_UTILITY)].map((m) => m[0])).toEqual(["bg-bg-card"])
    expect(declared.has("bg-elevated")).toBe(true)
  })
})
