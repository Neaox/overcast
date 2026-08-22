/**
 * classnames/prefer-use-resource-mutation
 *
 * docs/plans/web-ui-dry-refactor.md §7: "flag `useMutation(` in
 * `src/features/**`. Enforces P8. Needs an allowlist comment for the handful
 * of mutations that genuinely want custom `onError`."
 *
 * `useResourceMutation` (src/hooks/use-resource-mutation.ts) already wires
 * `invalidateQueries` plus the success/error toast pair; 21+ files under
 * `features/**` hand-roll `useMutation` + `invalidateQueries` + two toasts
 * instead, which is the exact duplication the audit's thesis names.
 *
 * The "allowlist comment" the plan asks for is a plain ESLint disable
 * comment — `// eslint-disable-next-line classnames/prefer-use-resource-mutation -- <reason>`
 * — rather than a second configuration surface: it already requires a
 * reason at the call site and shows up in a `grep`, which is what an
 * allowlist is for.
 *
 * ❌ src/features/foo/components/foo-page.tsx
 *      const del = useMutation({ mutationFn: deleteFoo, onSuccess() { … } })
 * ✅ const del = useResourceMutation({ mutationFn: deleteFoo, successMessage: "Foo deleted" })
 */

const FEATURES_DIR = /[\\/]src[\\/]features[\\/]/

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Prefer useResourceMutation over a raw useMutation() under src/features/** — disable per-line with a reason for the handful that need a custom onError",
    },
    schema: [],
    messages: {
      preferShared:
        "Use useResourceMutation (src/hooks/use-resource-mutation.ts) instead of a raw useMutation() — it already wires invalidateQueries and the two toasts. If this mutation genuinely needs a custom onError, disable this rule on the line with a comment explaining why.",
    },
  },

  create(context) {
    const filename = context.filename ?? context.getFilename()
    if (!FEATURES_DIR.test(filename)) return {}

    return {
      CallExpression(node) {
        if (node.callee.type === "Identifier" && node.callee.name === "useMutation") {
          context.report({ node: node.callee, messageId: "preferShared" })
        }
      },
    }
  },
}
