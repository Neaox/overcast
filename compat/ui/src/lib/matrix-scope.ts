/**
 * matrix-scope — tells a real gap (a suite that should implement a group but
 * has no result for it yet) apart from a cell that is structurally out of
 * scope (a group registry-scoped to other suites via `"suites"`, see
 * `compat/suites/registry.schema.json`).
 *
 * Before this, `ServiceTable` rendered both as the same empty cell — a `—`
 * placeholder (or, in interactive mode, a play button offering to run a test
 * the suite does not implement at all). `cdk-lifecycle` is scoped to `cdk`
 * today, but `cdk` is filtered out of the SDK matrix entirely (see `App.tsx`
 * `sdkSuites`) so the distinction has never been visible; this is the
 * general mechanism for the next `"suites"`-scoped group that lands inside
 * the SDK matrix rather than beside it.
 */

/** Is `suite` out of scope for a group whose registry entry declares
 * `suites`? `undefined` means the group is unscoped — every suite is
 * expected to implement it, so nothing is ever out of scope. */
export function isOutOfScope(
  groupSuites: readonly string[] | undefined,
  suite: string,
): boolean {
  return groupSuites !== undefined && !groupSuites.includes(suite);
}
