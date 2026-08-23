import { readFileSync } from "node:fs"
import { sourceFiles } from "@/test/source-files"

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
 * Tailwind's own palette (`text-blue-400`, `bg-amber-500/15`) stays out of
 * scope *here* — those utilities do render, so a raw hue is not the failure
 * this pattern is about. It is forbidden for a different reason, by the
 * raw-hue allowlist further down.
 *
 * `cat` and `scrim` are ours: the categorical identity ramp (`text-cat-7`) and
 * the in-card dim wash (`bg-scrim-dim`). They are listed so that a slot outside
 * the declared ramp — `text-cat-11`, say — fails here instead of rendering as
 * inherited text.
 */
const COLOUR_UTILITY =
  /(?<![\w-])(?:bg|text|border|ring|outline|accent|fill|stroke|caret|placeholder|divide|from|via|to)-(bg|fg|accent|danger|warning|success|border|sidebar|cloud|cat|scrim|muted|primary|secondary|surface|card|popover|destructive|input|foreground|background|ring)[a-z0-9-]*\b/g

/**
 * Every file under `src/` with its text, read once and shared by both scans
 * below. Two independent walks of ~1000 files is enough I/O on a cold cache to
 * push a 5s test over, and neither scan needs anything the other doesn't.
 *
 * `global.test.ts` is excluded at the source: it quotes undeclared tokens and
 * raw hues on purpose, as the counterexamples each pattern is tested against.
 */
let sourceTextCache: Array<{ file: string; text: string }> | undefined
function sourceTexts(): Array<{ file: string; text: string }> {
  sourceTextCache ??= [...sourceFiles("src")]
    .filter((file) => !file.endsWith("global.test.ts"))
    .map((file) => ({ file, text: readFileSync(file, "utf8") }))
  return sourceTextCache
}

/** Names declared as `--color-<name>` inside global.css's `@theme { … }` block. */
function declaredThemeColours(css: string): Set<string> {
  const start = css.indexOf("@theme {")
  expect(start).toBeGreaterThanOrEqual(0)
  const block = css.slice(start, css.indexOf("}", start))
  return new Set([...block.matchAll(/--color-([a-z0-9-]+)\s*:/g)].map((m) => m[1]))
}

describe("Prism token theme", () => {
  // Which selectors exist is pinned by token-theme-coverage.test.ts, derived
  // from TOKEN_COLOR_CLASSES rather than restated here — a hand-kept mirror
  // of that list is exactly the copy-drift log-format.ts documents.

  it("keeps dark-mode number tokens orange", () => {
    const css = readFileSync("src/styles/syntax-tokens.css", "utf8")

    // The dark palette's number value stays the orange it has always been —
    // and is not the keyword purple, which is the regression this test was
    // written against.
    expect(css).toMatch(/--_dark-token-number: oklch\(0\.75 0\.14 55\);/)
    expect(css).not.toMatch(/--_dark-token-number: oklch\(0\.72 0\.15 290\);/)
    // Both dark scopes (explicit choice and system preference) wire the
    // variable to that palette value.
    expect([...css.matchAll(/--token-number: var\(--_dark-token-number\);/g)]).toHaveLength(2)
  })
})

/* A categorical slot declared in only one theme is the exact failure the ramp
   exists to prevent: it renders as a fixed hue tuned for whichever theme got the
   value, which is what `text-emerald-300` on a near-black console already was. */
describe("categorical identity ramp", () => {
  const css = readFileSync("src/styles/global.css", "utf8")
  const slots = [...css.matchAll(/--color-(cat-\d+)\s*:/g)].map((m) => m[1])

  it("exposes ten slots as Tailwind utilities", () => {
    expect(slots).toEqual(Array.from({ length: 10 }, (_, i) => `cat-${i + 1}`))
  })

  // A `var(…)` alias in the dark selectors is not a value; only the literal in
  // the light block and the one in the --_dark-* palette block count.
  it.each([
    ["light", "--"],
    ["dark", "--_dark-"],
  ])("gives every slot its own %s value", (_theme, prefix) => {
    const missing = slots.filter((slot) => !css.includes(`${prefix}${slot}: oklch(`))
    expect(missing).toEqual([])
  })

  it("gives the in-card dim wash a value in both themes", () => {
    expect(css).toContain("--scrim-dim: rgb(")
    expect(css).toContain("--_dark-scrim-dim: rgb(")
  })

  it("wires each dark selector to the dark palette", () => {
    expect([...css.matchAll(/--cat-1: var\(--_dark-cat-1\);/g)]).toHaveLength(2)
  })
})

/* Tailwind silently emits nothing for a colour utility whose `--color-*` variable
   isn't declared, so a typo'd or invented token (bg-bg-card, text-fg-accent) fails
   open: the element renders transparent/inherited instead of erroring at build. */
describe("semantic colour tokens", () => {
  it("resolves every colour utility used in src/ to a --color-* declared in @theme", () => {
    const declared = declaredThemeColours(readFileSync("src/styles/global.css", "utf8"))
    const unresolved: string[] = []

    for (const { file, text } of sourceTexts()) {
      text.split("\n").forEach((line, i) => {
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

/**
 * docs/plans/palette-categorical-tokens.md requirement 8.
 *
 * A raw Tailwind hue renders fine — which is exactly why it needs a test rather
 * than failing at build. `text-purple-400` is one fixed value painted on
 * `#F4F8FC` and on `#10161D` alike, so it can only ever be tuned for one theme
 * and tolerated in the other. Every colour in `src/` now resolves through a
 * token carrying its own light and dark value: state onto `--success` /
 * `--warning` / `--danger` / `--accent`, identity onto the `--cat-*` ramp,
 * syntax onto `--token-*`.
 *
 * ALLOWLIST is empty, and the intent is that it stays that way. Adding an entry
 * is a decision that some hue genuinely cannot be a token — not a way to land a
 * call site in a hurry. Entries are `src/…:class`, never a bare file, so an
 * exemption covers one known site rather than opening a whole file up.
 */
describe("raw Tailwind palette classes (docs/plans/palette-categorical-tokens.md §8)", () => {
  const RAW_HUE =
    /(?<![\w-])(?:bg|text|border(?:-[xytblrse])?|ring(?:-offset)?|outline|accent|fill|stroke|caret|placeholder|divide(?:-[xy])?|from|via|to|shadow|decoration)-(?:red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone)-(?:50|[1-9]00|950)(?![\d])(?:\/\d+)?/g

  /** `src/…:class` sites allowed to keep a raw hue. Each needs a stated reason. */
  const ALLOWLIST = new Set<string>([])

  /**
   * Comments are prose about the migration, not code the browser paints — the
   * files explaining *why* a hue was wrong necessarily name one. Stripping them
   * is also what stops this test being the reason nobody can write the sentence
   * "`text-yellow-400` disappears on a light row".
   */
  function stripComments(source: string): string {
    return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/\/\/[^\n]*/g, "")
  }

  it("uses no raw Tailwind palette class in src/ outside the allowlist", () => {
    const offenders: string[] = []

    for (const { file, text } of sourceTexts()) {
      const rel = file.replace(/\\/g, "/")
      const key = rel.slice(rel.lastIndexOf("src/"))
      stripComments(text)
        .split("\n")
        .forEach((line, i) => {
          for (const match of line.matchAll(RAW_HUE)) {
            if (ALLOWLIST.has(`${key}:${match[0].replace(/\/\d+$/, "")}`)) continue
            offenders.push(`${file}:${i + 1}  ${match[0]}`)
          }
        })
    }

    expect(
      offenders,
      "Raw Tailwind palette classes are theme-blind — use a --cat-*, --token-* or\n" +
        "semantic token instead (docs/plans/palette-categorical-tokens.md):\n" +
        offenders.join("\n"),
    ).toEqual([])
  })

  // guard-the-guard: the pattern has to catch the shapes that actually occurred
  // in the 429 sites this replaced, and leave the token vocabulary alone.
  it.each([
    ["text-purple-400", "text-purple-400"],
    ["bg-amber-400/10", "bg-amber-400/10"],
    ["hover:bg-emerald-500/15", "bg-emerald-500/15"],
    ["dark:text-yellow-400", "text-yellow-400"],
    ["shadow-amber-950/20", "shadow-amber-950/20"],
    ["border-l-red-500/60", "border-l-red-500/60"],
    ["from-teal-400/8", "from-teal-400/8"],
  ])("catches %s", (source, expected) => {
    expect([...source.matchAll(RAW_HUE)].map((m) => m[0])).toEqual([expected])
  })

  it.each([
    "text-cat-7",
    "bg-success/15",
    "text-fg-muted",
    "token string",
    "bg-accent/15",
    "border-l-danger/60",
  ])("leaves the token vocabulary alone: %s", (source) => {
    expect([...source.matchAll(RAW_HUE)]).toEqual([])
  })

  it("does not read hues out of comments", () => {
    const src = ["// text-blue-400 was theme-blind", "/* bg-amber-500/15 */", "const a = 1"].join(
      "\n",
    )
    expect([...stripComments(src).matchAll(RAW_HUE)]).toEqual([])
  })
})

describe("Scrollbar styling", () => {
  it("uses progressive enhancement for subtle scrollbars", () => {
    const css = readFileSync("src/styles/global.css", "utf8")

    expect(css).toMatch(/scrollbar-width:\s*thin/)
    expect(css).toMatch(/scrollbar-color:\s*var\(--border\) transparent/)
    expect(css).toMatch(/::-webkit-scrollbar-track\s*\{\s*background:\s*transparent/)
  })
})

/**
 * docs/plans/web-ui-dry-refactor.md §7 ("Guardrails") calls these five patterns
 * out by name as the bespoke shapes the new `ResourceTable`/`ResourceListPage`/
 * `ResourceListSection` scaffolds were built to replace, and asks for a ratchet
 * rather than a lint rule for the migration period: each ceiling below permits
 * every existing site, forbids the count from growing, and its failure message
 * names the file+line that pushed it over. Lower a ceiling in the same commit
 * that removes the sites it covered — never bump one up to make room for a new
 * bespoke site; use the shared component instead (see AGENTS.md).
 *
 * Baselines were captured 2026-08-23, the day #1200 (waves 1-2) finished
 * converting every Archetype-C index page to the shared scaffolds.
 */
describe("DRY-refactor ratchets (docs/plans/web-ui-dry-refactor.md §7)", () => {
  /** Every match of `pattern` across `dir`, as `"file:line  matchedText"`. */
  function patternHits(
    pattern: RegExp,
    dir: string,
    exclude: (file: string) => boolean = () => false,
  ) {
    const hits: string[] = []
    for (const file of sourceFiles(dir)) {
      // this file's own prose/regex literals are not the code under test
      if (file.endsWith("global.test.ts") || exclude(file)) continue
      readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          for (const match of line.matchAll(pattern)) {
            hits.push(`${file}:${i + 1}  ${match[0]}`)
          }
        })
    }
    return hits
  }

  it("does not grow the raw <Spinner> element count", () => {
    // The 14-16px busy indicator, used directly rather than through
    // Button.busy — legitimate inside a chip/badge/toast, but every site is a
    // candidate for the P4 busy-button rollout to absorb.
    const hits = patternHits(/<Spinner\b/g, "src")
    expect(hits.length, `<Spinner> sites:\n${hits.join("\n")}`).toBeLessThanOrEqual(209)
  })

  it("does not grow the hand-rolled disabled={…isPending…} count", () => {
    // Button.busy encodes "disabled while pending" once; each of these sites
    // re-derives it locally instead of adopting the prop.
    const hits = patternHits(/disabled=\{[^}]*isPending[^}]*\}/g, "src")
    expect(hits.length, `disabled={…isPending…} sites:\n${hits.join("\n")}`).toBeLessThanOrEqual(
      131,
    )
  })

  it("does not grow the number of local DetailRow/InfoRow/MetaRow definitions", () => {
    // Directly mirrors the classnames/no-local-detail-row eslint rule; kept
    // here too because a ratchet names the exact remaining file, not just "a
    // file somewhere".
    const hits = patternHits(/\bfunction\s+(?:DetailRow|InfoRow|MetaRow)\b/g, "src/features")
    expect(
      hits.length,
      `local DetailRow/InfoRow/MetaRow definitions:\n${hits.join("\n")}`,
    ).toBeLessThanOrEqual(1)
  })

  it("does not grow the raw useMutation( count in features/**", () => {
    // useResourceMutation (src/hooks/use-resource-mutation.ts, outside
    // features/**) already folds invalidateQueries + the two toasts; each hit
    // here hand-rolls that instead.
    const hits = patternHits(/\buseMutation\s*\(/g, "src/features")
    expect(hits.length, `raw useMutation( sites:\n${hits.join("\n")}`).toBeLessThanOrEqual(80)
  })

  it("does not grow the raw <Loader2 count outside ui/primitives.tsx", () => {
    // ui/primitives.tsx is Spinner's own implementation. Every other site
    // reaches past Spinner for the bare lucide-react icon; the plan's target
    // is 0, not yet reached because P4 (Button.busy rollout) hasn't landed —
    // this ceiling holds the line at today's count until it does.
    const hits = patternHits(/<Loader2\b/g, "src", (file) =>
      file.replace(/\\/g, "/").endsWith("components/ui/primitives.tsx"),
    )
    expect(
      hits.length,
      `<Loader2 sites outside ui/primitives.tsx:\n${hits.join("\n")}`,
    ).toBeLessThanOrEqual(9)
  })
})
