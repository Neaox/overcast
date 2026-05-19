/**
 * classnames/no-redundant-cn
 *
 * Disallow cn() calls that contain only a single static string argument.
 * cn() (clsx + tailwind-merge) adds runtime overhead to resolve class
 * conflicts. When there's a single static string with no conditions or
 * className prop to merge, plain JSX is sufficient — Tailwind's JIT scanner
 * finds the classes either way.
 *
 * ❌ className={cn("flex gap-2 text-sm")}
 * ✅ className="flex gap-2 text-sm"
 */

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description: "Disallow cn() with a single static string argument",
    },
    fixable: "code",
    messages: {
      redundantCn: "cn() with a single static string is redundant; use a plain className.",
    },
    schema: [],
  },

  create(context) {
    return {
      JSXAttribute(node) {
        if (node.name.name !== "className") return
        const value = node.value
        if (!value || value.type !== "JSXExpressionContainer") return
        const expr = value.expression
        if (expr.type !== "CallExpression") return

        // Check for cn() call
        if (!isCnCall(expr)) return

        const args = expr.arguments
        if (args.length !== 1) return
        const arg = args[0]
        if (arg.type !== "Literal" || typeof arg.value !== "string") return

        context.report({
          node: expr,
          messageId: "redundantCn",
          fix(fixer) {
            // Replace the entire JSXExpressionContainer {cn("...")} with "..."
            // We need to replace node.value (the JSXExpressionContainer) with a string literal
            return fixer.replaceText(node.value, JSON.stringify(arg.value))
          },
        })
      },
    }
  },
}

function isCnCall(node) {
  return node.callee && node.callee.type === "Identifier" && node.callee.name === "cn"
}
