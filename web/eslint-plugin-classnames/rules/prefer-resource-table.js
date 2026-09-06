/**
 * classnames/prefer-resource-table
 *
 * #1327 Wave E: "an ESLint `no-restricted-imports` entry for
 * `@/components/ui/table` outside `components/ui/**`, with an explicit
 * allowlist for the deliberately bespoke sites — so a new bespoke table fails
 * lint unless it is added to the list with a reason. Cheap, and it is what
 * actually stops drift."
 *
 * ESLint is gone (#1330), so this is that entry as a house rule instead. It
 * fires on an import of the table primitives from anywhere under
 * `src/features/**`: fifty-five hand-rolled `<Table>` sites accumulated before
 * CONTRIBUTING § Tables existed, and the six waves that converted them are only
 * worth the effort if the count stops growing.
 *
 * The allowlist is a disable comment at the import, the same shape
 * `prefer-use-resource-mutation` uses — it demands a reason at the site rather
 * than in a second configuration file, and `grep` finds every one of them:
 *
 *   // eslint-disable-next-line classnames/prefer-resource-table -- <why>
 *
 * That reason sits beside the "ResourceTable didn't fit because …" comment
 * CONTRIBUTING already asks for at the call site; the disable line is what makes
 * the linter agree with the prose.
 *
 * ❌ src/features/foo/components/foo-list.tsx
 *      import { Table, TableBody, TableRow } from "@/components/ui/table"
 * ✅ import { ResourceTable } from "@/components/ui/resource-table"
 */

const FEATURES_DIR = /[\\/]src[\\/]features[\\/]/
const TABLE_MODULE = "@/components/ui/table"

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Prefer ResourceTable over composing the table primitives under src/features/** — disable per-line with a reason for a table that genuinely does not fit",
    },
    schema: [],
    messages: {
      preferResourceTable:
        "Compose this through ResourceTable (components/ui/resource-table.tsx) rather than the table primitives — it owns the loading/empty/filtered/error states, sorting, row actions and the delete flow. A table that genuinely does not fit (a result grid, a live stream, the debug page) disables this rule on the line with a reason, and says the same thing in a comment at the call site. See CONTRIBUTING § Tables and #1327.",
    },
  },

  create(context) {
    const filename = context.filename ?? context.getFilename()
    if (!FEATURES_DIR.test(filename)) return {}

    return {
      ImportDeclaration(node) {
        if (node.source.value !== TABLE_MODULE) return
        // A type-only import renders nothing — it is a caller borrowing a prop
        // type, not a hand-rolled table.
        if (node.importKind === "type") return
        context.report({ node, messageId: "preferResourceTable" })
      },
    }
  },
}
