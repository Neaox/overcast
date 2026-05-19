/**
 * classnames/no-bare-ternary
 *
 * Disallow bare ternary/logical expressions in className attributes that
 * aren't wrapped in cn(). Wrapping in cn() enables:
 *   - tailwind-merge conflict resolution (e.g. a parent passes "p-4" that
 *     should override the component's default "p-2")
 *   - Consistent pattern across the codebase — every conditional className
 *     reads the same way
 *   - Simpler "cond && classes" instead of "cond ? classes : """
 *
 * ❌ className={active ? "bg-accent text-white" : "text-muted"}
 * ✅ className={cn(active ? "bg-accent text-white" : "text-muted")}
 *
 * ❌ className={isFifo ? "pr-14" : ""}
 * ✅ className={cn(isFifo && "pr-14")}
 */

import { ensureCnImport } from "../ensure-cn-import.js"

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description: "Disallow bare ternary/logical expressions in className; wrap in cn()",
    },
    fixable: "code",
    messages: {
      wrapInCn: "Wrap conditional className in cn() for consistent merging.",
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

        // Skip if already wrapped in cn() or any call expression
        if (expr.type === "CallExpression") return
        // Skip template literals (handled by no-template-literal)
        if (expr.type === "TemplateLiteral") return
        // Skip plain identifiers/member expressions (className={styles.foo})
        if (expr.type === "Identifier" || expr.type === "MemberExpression") return

        // Target ternaries and logical expressions
        if (expr.type !== "ConditionalExpression" && expr.type !== "LogicalExpression") {
          return
        }

        const sourceCode = context.sourceCode || context.getSourceCode()

        context.report({
          node: expr,
          messageId: "wrapInCn",
          *fix(fixer) {
            if (expr.type === "ConditionalExpression") {
              const testText = sourceCode.getText(expr.test)
              const consText = sourceCode.getText(expr.consequent)
              const altText = sourceCode.getText(expr.alternate)
              const consIsEmpty = isEmptyString(expr.consequent, consText)
              const altIsEmpty = isEmptyString(expr.alternate, altText)

              if (altIsEmpty) {
                // Wrap compound tests (e.g. a || b) in parens — && binds tighter than ||
                const wrappedTest = wrapIfNeeded(testText, expr.test)
                yield fixer.replaceText(expr, `cn(${wrappedTest} && ${consText})`)
              } else if (consIsEmpty) {
                const wrappedTest = wrapIfNeeded(testText, expr.test)
                yield fixer.replaceText(expr, `cn(!${wrappedTest} && ${altText})`)
              } else {
                yield fixer.replaceText(expr, `cn(${sourceCode.getText(expr)})`)
              }
            } else {
              // LogicalExpression — just wrap
              yield fixer.replaceText(expr, `cn(${sourceCode.getText(expr)})`)
            }
            yield* ensureCnImport(fixer, context)
          },
        })
      },
    }
  },
}

function isEmptyString(node, text) {
  return node.type === "Literal" && node.value === "" && (text === '""' || text === "''")
}

function wrapIfNeeded(text, node) {
  if (
    node.type === "LogicalExpression" ||
    node.type === "BinaryExpression" ||
    node.type === "ConditionalExpression"
  ) {
    return `(${text})`
  }
  return text
}
