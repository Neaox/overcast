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
 * global explicitly, then register every grammar inline — no side-effect
 * import needed. The grammars themselves live in
 * [prism-grammars.ts](./prism-grammars.ts); registering them here, in the
 * one module that imports "prismjs", is what guarantees every consumer —
 * main thread and highlight worker alike — sees the same registry.
 */
// Order matters: the config module must run before prismjs's body, which
// reads the flag it sets — see prism-global-config.ts for the worker story.
import "./prism-global-config"
import Prism from "prismjs"
import { registerGrammars } from "./prism-grammars"

// Ensure Prism is on the global scope (redundant in dev, required in prod).
if (typeof window !== "undefined") {
  ;(window as unknown as Record<string, unknown>).Prism = Prism
}

registerGrammars(Prism)

export default Prism
