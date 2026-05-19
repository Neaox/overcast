/**
 * PrismJS barrel — import Prism from here instead of "prismjs" directly.
 *
 * Why: PrismJS language plugins (e.g. `prismjs/components/prism-json`) are
 * plain scripts that reference the bare `Prism` global at the top level.
 * Vite's production build (Rollup) wraps CJS modules in lazy factories — the
 * factory that calls `window.Prism = Prism` only executes when the ESM binding
 * is first accessed.  Since language plugins don't `import` from "prismjs"
 * (they assume the global exists), `Prism` may still be `undefined` when the
 * plugin code runs, causing "Prism is not defined".
 *
 * Fix: import the default export (which triggers the lazy factory), set the
 * global explicitly, then register the JSON grammar inline — no side-effect
 * import needed.
 */
import Prism from "prismjs"

// Ensure Prism is on the global scope (redundant in dev, required in prod).
if (typeof window !== "undefined") {
  ;(window as unknown as Record<string, unknown>).Prism = Prism
}

// Register JSON grammar (source: prismjs/components/prism-json, MIT license).
// Copied here to avoid the side-effect import that relies on the global.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- json may not be registered yet at runtime
Prism.languages.json ??= {
  property: {
    pattern: /(^|[^\\])"(?:\\.|[^\\"\r\n])*"(?=\s*:)/,
    lookbehind: true,
    greedy: true,
  },
  string: {
    pattern: /(^|[^\\])"(?:\\.|[^\\"\r\n])*"(?!\s*:)/,
    lookbehind: true,
    greedy: true,
  },
  comment: {
    pattern: /\/\/.*|\/\*[\s\S]*?(?:\*\/|$)/,
    greedy: true,
  },
  number: /-?\b\d+(?:\.\d+)?(?:e[+-]?\d+)?\b/i,
  punctuation: /[{}[\],]/,
  operator: /:/,
  boolean: /\b(?:false|true)\b/,
  null: {
    pattern: /\bnull\b/,
    alias: "keyword",
  },
}
Prism.languages.webmanifest = Prism.languages.json

export default Prism
