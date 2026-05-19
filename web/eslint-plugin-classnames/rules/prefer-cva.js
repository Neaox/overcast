/**
 * classnames/prefer-cva
 *
 * Detect cn() calls in className that are complex enough to benefit from
 * class-variance-authority (cva). Flags calls that contain multiple
 * conditional expressions (ternaries or logical-&&) suggesting the
 * component has variant-like behaviour that cva can model more clearly.
 *
 * This is a **report-only** rule — no autofix. Refactoring to cva is a
 * manual, design-level change.
 *
 * Heuristics (any one triggers the warning):
 *   1. ≥ N conditional arguments (ternary or &&) inside a single cn() call.
 *      Default N = 3.
 *   2. The same identifier appears as the test of ≥ 2 separate conditional
 *      arguments (strong signal of a "variant" dimension).
 *
 * ❌ cn("base", active ? "bg-accent" : "bg-muted", active && "font-bold", size === "lg" ? "p-4" : "p-2")
 *    → 3 conditionals + "active" tested twice → cva candidate
 *
 * ✅ cn("base", active && "ring-2")
 *    → 1 conditional, fine as-is
 *
 * ✅ cn(myVariants({ size, color }), className)
 *    → already using cva/variants
 */

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Suggest using cva (class-variance-authority) when cn() has many conditional class arguments",
    },
    fixable: null, // no autofix
    messages: {
      preferCva:
        "This cn() call has {{count}} conditional arguments — consider extracting variants with cva().",
      repeatedCondition:
        "The condition '{{name}}' drives {{count}} arguments in this cn() call — this is a variant dimension that cva() can model.",
    },
    schema: [
      {
        type: "object",
        properties: {
          /** Minimum number of conditional args to trigger the "too complex" warning. */
          minConditions: { type: "integer", minimum: 2, default: 3 },
          /** Minimum repeats of the same condition to trigger the "variant" warning. */
          minRepeats: { type: "integer", minimum: 2, default: 2 },
        },
        additionalProperties: false,
      },
    ],
  },

  create(context) {
    const opts = context.options[0] || {}
    const minConditions = opts.minConditions ?? 3
    const minRepeats = opts.minRepeats ?? 2

    /**
     * Return a stable string key for a condition expression so we can
     * detect the same condition being used multiple times.
     */
    function conditionKey(node) {
      const sourceCode = context.sourceCode || context.getSourceCode()
      // For simple identifiers / member expressions, use the source text.
      // For binary expressions (=== / !==) use just the left side.
      if (node.type === "Identifier") return node.name
      if (node.type === "MemberExpression") return sourceCode.getText(node)
      if (node.type === "UnaryExpression" && node.operator === "!") {
        return conditionKey(node.argument)
      }
      if (
        node.type === "BinaryExpression" &&
        (node.operator === "===" ||
          node.operator === "!==" ||
          node.operator === "==" ||
          node.operator === "!=")
      ) {
        return sourceCode.getText(node.left)
      }
      return null
    }

    /**
     * Check if a node is a conditional expression (ternary or logical-&&)
     * that's being used to conditionally apply classes.
     */
    function isConditionalArg(node) {
      return (
        node.type === "ConditionalExpression" ||
        (node.type === "LogicalExpression" && node.operator === "&&")
      )
    }

    /** Extract the "test" / "condition" node from a conditional. */
    function getCondition(node) {
      if (node.type === "ConditionalExpression") return node.test
      if (node.type === "LogicalExpression" && node.operator === "&&") return node.left
      return null
    }

    return {
      CallExpression(node) {
        // Only look at cn(...) calls
        if (node.callee.type !== "Identifier" || node.callee.name !== "cn") return

        // Must be inside a className attribute
        let parent = node.parent
        while (parent) {
          if (parent.type === "JSXAttribute" && parent.name && parent.name.name === "className") {
            break
          }
          parent = parent.parent
        }
        if (!parent) return

        const args = node.arguments
        const conditionals = args.filter(isConditionalArg)

        // Heuristic 1: too many conditional arguments
        if (conditionals.length >= minConditions) {
          context.report({
            node,
            messageId: "preferCva",
            data: { count: String(conditionals.length) },
          })
          return // one report per cn() call is enough
        }

        // Heuristic 2: same condition tested in multiple arguments
        const condCounts = new Map()
        for (const arg of conditionals) {
          const cond = getCondition(arg)
          if (!cond) continue
          const key = conditionKey(cond)
          if (!key) continue
          condCounts.set(key, (condCounts.get(key) || 0) + 1)
        }

        for (const [name, count] of condCounts) {
          if (count >= minRepeats) {
            context.report({
              node,
              messageId: "repeatedCondition",
              data: { name, count: String(count) },
            })
            return // one report per cn() call
          }
        }
      },
    }
  },
}
