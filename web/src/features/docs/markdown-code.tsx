/**
 * The one fenced-code renderer behind the console's markdown surfaces — the
 * /docs route and the per-service docs modal both map ReactMarkdown's `pre`
 * to `MarkdownCodeBlock`, so a fence renders the same way (and through the
 * same highlight kernel, via the shared `HighlightedCode` component)
 * everywhere docs markdown appears.
 *
 * Fence-name resolution is this module's policy: `fenceLanguage` maps the
 * fence's `language-xxx` class to a registered Prism grammar, or to null —
 * `HighlightedCode`'s render-plain contract — for anything else (mermaid,
 * text, a language nobody registered). An unknown fence therefore degrades
 * to styled plain text; it can never error or render unstyled.
 */
import { isValidElement, type ReactNode } from "react"
import { HighlightedCode } from "@/components/ui/highlighted-code"
import Prism from "@/lib/prism"

/**
 * The one home for fence-alias normalization: every alias the docs' fences
 * use, mapped to its grammar's canonical name. Prism registers most of these
 * as registry aliases too (`Prism.languages.sh` IS the bash grammar), but
 * resolving to the canonical name here keeps the highlight kernel's
 * `(text, language)` cache keyed one way however a fence spells it.
 *
 * tsx deliberately maps to typescript rather than a real tsx grammar:
 * Prism's jsx/tsx components rewrite tokens in an `after-tokenize` hook,
 * which `Prism.highlight` (the markup backend) runs but `Prism.tokenize`
 * (the ranges backend) does not — registering them would break the
 * backend-parity contract pinned by token-theme-coverage.test.ts.
 */
const FENCE_ALIASES: Record<string, string> = {
  sh: "bash",
  shell: "bash",
  js: "javascript",
  jsonc: "json",
  ts: "typescript",
  tsx: "typescript",
  yml: "yaml",
  py: "python",
  cs: "csharp",
  html: "markup",
  xml: "markup",
}

/**
 * The Prism language a fence's `language-xxx` class resolves to, or null —
 * the render-plain policy — when there is no class or no registered grammar.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function fenceLanguage(className: string | undefined): string | null {
  const match = /(?:^|\s)language-([\w+.-]+)/.exec(className ?? "")
  if (!match) return null
  const fence = match[1].toLowerCase()
  const language = FENCE_ALIASES[fence] ?? fence
  // The grammar record's typing claims every key exists; missing languages
  // are real at runtime, hence the assertion (same note as the kernel). The
  // core bundle also registers `plain`/`plaintext`/`text`/`txt` as one empty
  // grammar — highlighting those is a no-op, so resolve them to the explicit
  // render-plain policy too.
  const grammar = Prism.languages[language] as Prism.Grammar | undefined
  return grammar !== undefined && grammar !== Prism.languages.plain ? language : null
}

/**
 * Both surfaces' block styling — the service-docs modal's original `<pre>`
 * look (border, muted background, mono, text-xs), now shared with /docs.
 * Handed to `HighlightedCode`, whose contract keeps it identical on every
 * backend/deferral branch.
 */
const CODE_BLOCK_CLASSES =
  "overflow-auto rounded-md border border-border bg-bg-muted p-3 font-mono text-xs text-fg"

/**
 * `pre` entry for a ReactMarkdown components map. A markdown code block
 * reaches `pre` as a single `<code>` child (rendered through whatever `code`
 * component the surface maps — its `className`/`children` props survive
 * either way); anything else keeps a plain styled `<pre>`.
 */
export function MarkdownCodeBlock({ children }: { children?: ReactNode }) {
  const code = fencedCode(children)
  // tabIndex: a long line scrolls sideways inside the block, and only a focusable
  // container can be scrolled by keyboard.
  if (code === null)
    return (
      <pre tabIndex={0} className={CODE_BLOCK_CLASSES}>
        {children}
      </pre>
    )
  return (
    <HighlightedCode
      // Fenced content always carries the fence's trailing newline; rendering
      // it would pad every block with an empty last line.
      text={code.text.replace(/\n$/, "")}
      language={fenceLanguage(code.className)}
      className={CODE_BLOCK_CLASSES}
    />
  )
}

function fencedCode(children: ReactNode): { className?: string; text: string } | null {
  if (!isValidElement(children)) return null
  const { className, children: text } = children.props as {
    className?: string
    children?: ReactNode
  }
  return typeof text === "string" ? { className, text } : null
}
