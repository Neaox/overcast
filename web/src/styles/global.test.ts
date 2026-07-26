import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"

/**
 * Colour utilities Tailwind resolves via `--color-*` in the `@theme` block.
 *
 * Two things widen this beyond the obvious `bg-`/`text-`/`border-` on our own
 * roots, both because the narrow version failed open on real bugs:
 *
 * - **Every colour-taking prefix**, not three. `hover:bg-surface-hover`,
 *   `focus-within:ring-ring` and `accent-primary` all rendered as nothing and
 *   all sailed past the old pattern; the lookbehind is what lets a variant
 *   prefix (`hover:`, `data-[…]:`) sit in front of the utility.
 * - **Roots that only *look* like ours** — the shadcn vocabulary this codebase
 *   was scaffolded from (`muted-foreground`, `primary`, `surface`, `input`,
 *   `card`). None are declared, so each is an invisible element, and none of
 *   them can ever become valid: they are not the names we chose.
 *
 * Tailwind's own palette (`text-blue-400`, `bg-amber-500/15`) is deliberately
 * still out of scope — that is the separate palette-collapse task, and those
 * utilities do render.
 */
const COLOUR_UTILITY =
  /(?<![\w-])(?:bg|text|border|ring|outline|accent|fill|stroke|caret|placeholder|divide|from|via|to)-(bg|fg|accent|danger|warning|success|border|sidebar|cloud|muted|primary|secondary|surface|card|popover|destructive|input|foreground|background|ring)[a-z0-9-]*\b/g

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

  it.each([
    ["hover:bg-surface-hover", "bg-surface-hover"],
    ["focus-within:ring-ring", "ring-ring"],
    ["accent-primary", "accent-primary"],
    ["text-muted-foreground", "text-muted-foreground"],
    ["data-[selected=true]:bg-card", "bg-card"],
  ])("flags %s, which the narrower pattern let through", (source, utility) => {
    expect([...source.matchAll(COLOUR_UTILITY)].map((m) => m[0])).toEqual([utility])
  })

  it("leaves Tailwind's own palette alone — those utilities do render", () => {
    expect([..."bg-amber-500/15 dark:text-blue-400".matchAll(COLOUR_UTILITY)]).toEqual([])
  })
})
