/**
 * local/prefer-shared-formatter
 *
 * docs/plans/web-ui-dry-refactor.md §7: "flag `toLocaleString` /
 * `toLocaleDateString` / `toLocaleTimeString` and any local
 * `function format(Bytes|Date|Duration)` outside `src/lib/format.ts`.
 * Enforces P10 and would have caught all four `formatBytes` copies."
 *
 * `src/lib/format.ts` already exports `formatBytes`/`formatDate`/
 * `formatTimeOfDay`/`formatPreciseTimeOfDay` and friends. Calling
 * `Intl`'s locale methods directly, or redefining a same-named local
 * formatter, is exactly the kind of parallel implementation this plan's
 * thesis warns about — each copy is one more place a formatting fix has to
 * be repeated.
 *
 * ❌ new Date(ts).toLocaleDateString()
 * ✅ formatDate(ts)
 *
 * ❌ function formatBytes(bytes: number) { … }   // in some feature file
 * ✅ import { formatBytes } from "@/lib/format"
 */

const FORMATTER_METHODS = new Set(["toLocaleString", "toLocaleDateString", "toLocaleTimeString"])
const LOCAL_FORMATTER_NAME = /^format(?:Bytes|Date|Duration)$/

function isSharedFormatterModule(filename) {
  return /[\\/]src[\\/]lib[\\/]format\.ts$/.test(filename)
}

function isComponentOrHelperFunction(node) {
  return node.type === "ArrowFunctionExpression" || node.type === "FunctionExpression"
}

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Prefer the shared formatters in src/lib/format.ts over toLocale*() calls or a locally redefined formatBytes/formatDate/formatDuration",
    },
    schema: [],
    messages: {
      preferSharedCall:
        "Use a shared formatter from src/lib/format.ts instead of calling {{name}} directly.",
      preferSharedFn:
        "'{{name}}' duplicates a formatter that already exists in src/lib/format.ts — import it instead of redefining it here.",
    },
  },

  create(context) {
    const filename = context.filename ?? context.getFilename()
    if (isSharedFormatterModule(filename)) return {}

    return {
      MemberExpression(node) {
        if (node.property.type !== "Identifier" || !FORMATTER_METHODS.has(node.property.name))
          return
        context.report({
          node: node.property,
          messageId: "preferSharedCall",
          data: { name: node.property.name },
        })
      },
      FunctionDeclaration(node) {
        if (node.id && LOCAL_FORMATTER_NAME.test(node.id.name)) {
          context.report({
            node: node.id,
            messageId: "preferSharedFn",
            data: { name: node.id.name },
          })
        }
      },
      VariableDeclarator(node) {
        if (
          node.id.type === "Identifier" &&
          LOCAL_FORMATTER_NAME.test(node.id.name) &&
          node.init &&
          isComponentOrHelperFunction(node.init)
        ) {
          context.report({
            node: node.id,
            messageId: "preferSharedFn",
            data: { name: node.id.name },
          })
        }
      },
    }
  },
}
