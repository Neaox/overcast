/**
 * local/no-duplicate-class-cluster
 *
 * docs/plans/web-ui-dry-refactor.md §7: "a generic rule that fails when a
 * literal class string over N tokens appears in more than M files. Would have
 * surfaced `font-mono text-sm font-medium text-fg` (56 sites) and
 * `grid grid-cols-2 gap-x-8 gap-y-3` (17 sites) before they were 56 and 17.
 * Highest leverage of the six, and the only one that catches *future*
 * clusters rather than known ones."
 *
 * Unlike the other five rules this one is inherently cross-file: "does this
 * literal class string exist widely across the tree" cannot be answered from
 * a single file's AST. Rather than rely on ESLint's own (unordered, partial)
 * file traversal to accumulate state, this rule reads the whole `src/` tree
 * itself with plain `node:fs` on first use and caches the result for the rest
 * of the process — one full-tree scan per `eslint` invocation, however many
 * files it lints — so every file sees the same, complete picture regardless
 * of visiting order.
 *
 * ❌ className="font-mono text-sm font-medium text-fg"  (already used this way
 *    in N+ other files) — extract a shared component/class instead of pasting
 *    the cluster into file N+1.
 * ✅ className={cn(sectionHeading, className)}
 *
 * The default thresholds (4-token window, appearing in >15 files) were tuned
 * against this repo's actual class-string distribution at the time the rule
 * was added: they surface a handful (~5) of genuine repeats rather than
 * flooding the log with every common 2-3 class combo (`flex items-center
 * gap-2` alone would swamp any lower N).
 */

import { readFileSync, readdirSync, statSync } from "node:fs"
import { join } from "node:path"

const DEFAULT_MIN_TOKENS = 4
const DEFAULT_MIN_FILES = 15

const CLASS_ATTR = /\bclassName="([^"{}]+)"/g
const CN_STRING_ARG = /\bcn\(\s*"([^"]+)"/g

function walk(dir, acc) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) walk(path, acc)
    else if (/\.tsx?$/.test(path)) acc.push(path)
  }
  return acc
}

/** Every literal class-attribute / cn() string found in `content`. */
function literalClassStrings(content) {
  const strings = []
  for (const m of content.matchAll(CLASS_ATTR)) strings.push(m[1])
  for (const m of content.matchAll(CN_STRING_ARG)) strings.push(m[1])
  return strings
}

/**
 * Builds { window -> Set<file> } across every `.ts`/`.tsx` file under
 * `srcDir`, for every contiguous `minTokens`-length run of space-separated
 * classes in every literal class string found.
 */
function buildClusterMap(srcDir, minTokens) {
  const map = new Map()
  for (const file of walk(srcDir, [])) {
    const seen = new Set(literalClassStrings(readFileSync(file, "utf8")))
    for (const raw of seen) {
      const tokens = raw.trim().split(/\s+/)
      for (let i = 0; i + minTokens <= tokens.length; i++) {
        const window = tokens.slice(i, i + minTokens).join(" ")
        let files = map.get(window)
        if (!files) map.set(window, (files = new Set()))
        files.add(file)
      }
    }
  }
  return map
}

// Cached across every file in one `eslint` process — the whole point is that
// a single-file view can never answer "is this cluster common", so the cost
// of the full-tree scan is paid once, not once per linted file.
let cached = null
function getClusterMap(srcDir, minTokens) {
  if (!cached || cached.srcDir !== srcDir || cached.minTokens !== minTokens) {
    cached = { srcDir, minTokens, map: buildClusterMap(srcDir, minTokens) }
  }
  return cached.map
}

/** The widest qualifying window inside `raw`, if any window's file count exceeds `minFiles`. */
function widestOffendingWindow(raw, minTokens, minFiles, map) {
  const tokens = raw.trim().split(/\s+/)
  let best = null
  for (let i = 0; i + minTokens <= tokens.length; i++) {
    const window = tokens.slice(i, i + minTokens).join(" ")
    const count = map.get(window)?.size ?? 0
    if (count > minFiles && (!best || count > best.count)) best = { window, count }
  }
  return best
}

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Flag a literal class string that already appears near-verbatim in many other files — extract a shared class/component instead of pasting another copy",
    },
    schema: [
      {
        type: "object",
        properties: {
          minTokens: { type: "integer", minimum: 2, default: DEFAULT_MIN_TOKENS },
          minFiles: { type: "integer", minimum: 2, default: DEFAULT_MIN_FILES },
          srcDir: { type: "string" },
        },
        additionalProperties: false,
      },
    ],
    messages: {
      duplicateCluster:
        'The {{tokens}}-class run "{{window}}" already appears in {{count}} other files — extract a shared class or component instead of adding another copy.',
    },
  },

  create(context) {
    const opts = context.options[0] || {}
    const minTokens = opts.minTokens ?? DEFAULT_MIN_TOKENS
    const minFiles = opts.minFiles ?? DEFAULT_MIN_FILES
    const srcDir = opts.srcDir ?? join(process.cwd(), "src")

    function check(node, raw) {
      const map = getClusterMap(srcDir, minTokens)
      const offender = widestOffendingWindow(raw, minTokens, minFiles, map)
      if (!offender) return
      context.report({
        node,
        messageId: "duplicateCluster",
        data: {
          window: offender.window,
          count: String(offender.count),
          tokens: String(minTokens),
        },
      })
    }

    return {
      JSXAttribute(node) {
        if (node.name.name !== "className") return
        const value = node.value
        if (value && value.type === "Literal" && typeof value.value === "string") {
          check(node, value.value)
        }
      },
      CallExpression(node) {
        if (node.callee.type !== "Identifier" || node.callee.name !== "cn") return
        const [first] = node.arguments
        if (first && first.type === "Literal" && typeof first.value === "string") {
          check(first, first.value)
        }
      },
    }
  },
}
