/**
 * The theming contract between the highlight kernel and syntax-tokens.css,
 * held by enumeration rather than trust:
 *
 * 1. Every token class the covered grammars can emit resolves to a themed
 *    colour class — so a grammar addition (or a newly covered language)
 *    cannot silently render uncoloured in the ranges backend.
 * 2. Every themed colour class has a `--token-*` variable in both palettes,
 *    a `.token.*` rule (markup backend), and a `::highlight()` rule (ranges
 *    backend) — all resolving through the same variable, which is what makes
 *    a theme switch recolour both backends with zero range work.
 * 3. The `::highlight()` rules are never comma-joined with class selectors:
 *    a browser without the Highlight API drops any selector list containing
 *    an unrecognised pseudo-element wholesale, which would uncolour the
 *    markup fallback in exactly the browsers that depend on it.
 */
import { readFileSync } from "node:fs"
import { grammarTokenClasses } from "./prism-ranges"
import {
  COVERED_TOKEN_LANGUAGES,
  TOKEN_COLOR_CLASSES,
  TOKEN_HIGHLIGHT_PREFIX,
  resolveTokenColorClass,
} from "./highlight-registry"

const css = readFileSync("src/styles/syntax-tokens.css", "utf8")

describe("grammar coverage", () => {
  for (const language of COVERED_TOKEN_LANGUAGES) {
    it(`themes every token class the ${language} grammar can emit`, () => {
      const classes = grammarTokenClasses(language)
      expect(classes.size).toBeGreaterThan(0)
      const uncovered = [...classes].filter((cls) => resolveTokenColorClass(cls) === null)
      expect(
        uncovered,
        `token classes with no colour: ${uncovered.join(", ")} — add them to ` +
          "TOKEN_COLOR_CLASSES and give each a --token-* variable + rules in syntax-tokens.css",
      ).toEqual([])
    })
  }
})

describe("token colour classes in syntax-tokens.css", () => {
  it.each([...TOKEN_COLOR_CLASSES])("themes '%s' in both palettes and both backends", (cls) => {
    // Both dark scopes (explicit [data-theme="dark"] and the system-dark
    // media block) wire the variable to the dark palette…
    expect(
      [...css.matchAll(new RegExp(`--token-${cls}: var\\(--_dark-token-${cls}\\);`, "g"))],
      `dark wiring for --token-${cls}`,
    ).toHaveLength(2)
    // …which defines it once…
    expect([...css.matchAll(new RegExp(`--_dark-token-${cls}:`, "g"))]).toHaveLength(1)
    // …and the light palette defines the remaining occurrence.
    expect([...css.matchAll(new RegExp(`--token-${cls}:`, "g"))]).toHaveLength(3)

    // Both selector families consume the variable: Prism markup spans…
    expect(css).toMatch(new RegExp(`\\.token\\.${cls} \\{\\s*color: var\\(--token-${cls}\\);`))
    // …and the ranges backend's registered highlight.
    expect(css).toMatch(
      new RegExp(
        `::highlight\\(${TOKEN_HIGHLIGHT_PREFIX}${cls}\\) \\{\\s*color: var\\(--token-${cls}\\);`,
      ),
    )
  })

  it("declares .token rules in TOKEN_COLOR_CLASSES order, so cascade and resolver agree", () => {
    // The two backends colour a multi-class token ("null keyword") through
    // different mechanisms: markup takes the LAST matching equal-specificity
    // rule in the stylesheet, ranges take the highest-indexed class in
    // TOKEN_COLOR_CLASSES (resolveTokenColorClass). They agree exactly when
    // the stylesheet declares rules in list order — this is that contract.
    const ruleOrder = [...css.matchAll(/^\s*\.token\.([a-z-]+) \{/gm)].map((m) => m[1])
    expect(ruleOrder).toEqual([...TOKEN_COLOR_CLASSES])
    // And the resolver honours the cascade for the one aliased pair the JSON
    // grammar actually emits.
    expect(resolveTokenColorClass("null keyword")).toBe("null")
    expect(resolveTokenColorClass("keyword null")).toBe("null")
  })

  it("keeps every ::highlight rule out of selector lists", () => {
    const withoutComments = css.replace(/\/\*[\s\S]*?\*\//g, "")
    const selectors = [...withoutComments.matchAll(/^\s*([^\n{}]*::highlight\([^)]+\)[^\n{]*)\{/gm)]
    expect(selectors.length).toBeGreaterThan(0)
    for (const match of selectors) {
      expect(match[1].trim(), "::highlight must be a lone selector").toMatch(
        /^::highlight\([a-z-]+\)$/,
      )
    }
  })
})
