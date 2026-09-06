/**
 * eslint-plugin-local — enforce idiomatic Tailwind className construction
 * in React components that use the cn() utility (clsx + tailwind-merge), plus
 * the docs/plans/web-ui-dry-refactor.md §7 guardrails that keep the
 * `ResourceTable`/`ResourceListPage`/`ResourceListSection` scaffolds (and
 * their sibling hooks/components) from being bypassed the way the code they
 * replaced was.
 *
 * These rules ensure that className attributes are written in a way that:
 *   - tailwind-merge can resolve class conflicts correctly
 *   - prettier-plugin-tailwindcss can sort classes
 *   - Tailwind's JIT content scanner sees all class tokens
 *   - Conditional styling is readable and maintainable
 *
 * Rules:
 *   local/no-template-literal          — use cn() instead of template literals
 *   local/no-concatenation              — use cn() instead of string concatenation
 *   local/no-bare-ternary               — wrap conditional classNames in cn()
 *   local/no-redundant-cn               — don't use cn() for a single static string
 *   local/no-dup-ternary                — hoist shared classes out of ternary branches
 *   local/prefer-cva                    — suggest cva() once a cn() call gets variant-shaped
 *   local/no-local-detail-row           — no local DetailRow/InfoRow/MetaRow under features/**
 *   local/prefer-button-busy            — use <Button busy> instead of disabled={isPending} + <Spinner>
 *   local/no-raw-spinner-in-content     — <Spinner> only inside <Button>/<Badge>/toast
 *   local/prefer-shared-formatter       — use src/lib/format.ts instead of toLocale*()/local formatBytes|Date|Duration
 *   local/prefer-use-resource-mutation  — use useResourceMutation instead of a raw useMutation() under features/**
 *   local/prefer-resource-table         — use ResourceTable instead of the table primitives under features/**
 *   local/no-duplicate-class-cluster    — flag a class-string run that already recurs across many files
 */

import noTemplateLiteral from "./rules/no-template-literal.js"
import noConcatenation from "./rules/no-concatenation.js"
import noBareTernary from "./rules/no-bare-ternary.js"
import noRedundantCn from "./rules/no-redundant-cn.js"
import noDupTernary from "./rules/no-dup-ternary.js"
import preferCva from "./rules/prefer-cva.js"
import noLocalDetailRow from "./rules/no-local-detail-row.js"
import preferButtonBusy from "./rules/prefer-button-busy.js"
import noRawSpinnerInContent from "./rules/no-raw-spinner-in-content.js"
import preferSharedFormatter from "./rules/prefer-shared-formatter.js"
import preferUseResourceMutation from "./rules/prefer-use-resource-mutation.js"
import preferResourceTable from "./rules/prefer-resource-table.js"
import noDuplicateClassCluster from "./rules/no-duplicate-class-cluster.js"

export default {
  rules: {
    "no-template-literal": noTemplateLiteral,
    "no-concatenation": noConcatenation,
    "no-bare-ternary": noBareTernary,
    "no-redundant-cn": noRedundantCn,
    "no-dup-ternary": noDupTernary,
    "prefer-cva": preferCva,
    "no-local-detail-row": noLocalDetailRow,
    "prefer-button-busy": preferButtonBusy,
    "no-raw-spinner-in-content": noRawSpinnerInContent,
    "prefer-shared-formatter": preferSharedFormatter,
    "prefer-use-resource-mutation": preferUseResourceMutation,
    "prefer-resource-table": preferResourceTable,
    "no-duplicate-class-cluster": noDuplicateClassCluster,
  },
}
