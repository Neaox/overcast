/**
 * local/no-local-detail-row
 *
 * docs/plans/web-ui-dry-refactor.md §7: "flag a local
 * `function DetailRow|InfoRow|MetaRow` in `src/features/**`. Directly
 * prevents P1's regression."
 *
 * Every one of these historically re-implemented the same
 * label/value-pair-with-optional-mono-value component that already exists
 * (or, once P1 lands, will exist as `DetailField` in `components/ui`).
 * Defining a second one under `features/` is exactly the fork this codebase's
 * convention (web/AGENTS.md, "Component conventions") asks callers not to
 * make.
 *
 * ❌ src/features/rds/components/instance-detail.tsx
 *      function DetailRow({ label, value }: { label: string; value: string }) { … }
 * ✅ import { DetailField } from "@/components/ui/detail-fields"
 */

const LOCAL_NAMES = new Set(["DetailRow", "InfoRow", "MetaRow"])
const FEATURES_DIR = /[\\/]src[\\/]features[\\/]/

function isComponentFunction(node) {
  return node.type === "ArrowFunctionExpression" || node.type === "FunctionExpression"
}

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Disallow a local DetailRow/InfoRow/MetaRow component under src/features/** — use the shared detail-field component instead",
    },
    schema: [],
    messages: {
      noLocalDetailRow:
        "'{{name}}' duplicates the shared detail-row pattern (components/ui) — extend the shared component instead of defining a local one under features/**.",
    },
  },

  create(context) {
    const filename = context.filename ?? context.getFilename()
    if (!FEATURES_DIR.test(filename)) return {}

    return {
      FunctionDeclaration(node) {
        if (node.id && LOCAL_NAMES.has(node.id.name)) {
          context.report({
            node: node.id,
            messageId: "noLocalDetailRow",
            data: { name: node.id.name },
          })
        }
      },
      VariableDeclarator(node) {
        if (
          node.id.type === "Identifier" &&
          LOCAL_NAMES.has(node.id.name) &&
          node.init &&
          isComponentFunction(node.init)
        ) {
          context.report({
            node: node.id,
            messageId: "noLocalDetailRow",
            data: { name: node.id.name },
          })
        }
      },
    }
  },
}
