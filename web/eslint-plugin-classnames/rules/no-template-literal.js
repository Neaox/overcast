/**
 * classnames/no-template-literal
 *
 * Disallow template literals in className attributes. Use cn() instead.
 *
 * Why this matters for Tailwind:
 *   - tailwind-merge cannot resolve class conflicts inside template literals
 *   - prettier-plugin-tailwindcss cannot sort classes inside interpolations
 *   - Conditional logic is cleaner as separate cn() arguments than ${} blocks
 *
 * Note: Tailwind's JIT scanner uses plain-text regex, so complete class tokens
 * inside template literal strings ARE still detected (e.g. "animate-spin").
 * The issue is tooling and maintainability, not class generation.
 *
 * ❌ className={`flex gap-2 ${isFetching ? "animate-spin" : ""}`}
 * ✅ className={cn("flex gap-2", isFetching && "animate-spin")}
 *
 * ❌ className={`flex ${active ? "bg-accent text-white" : "text-fg-muted"}`}
 * ✅ className={cn("flex", active ? "bg-accent text-white" : "text-fg-muted")}
 *
 * ❌ className={`flex ${className ?? ""}`}
 * ✅ className={cn("flex", className)}
 */

import { ensureCnImport } from "../ensure-cn-import.js"

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description: "Disallow template literals in className; use cn() instead",
    },
    fixable: "code",
    messages: {
      usesCn: "Use cn() instead of template literal for className.",
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
        if (expr.type !== "TemplateLiteral") return

        // Collect quasis (static parts) and expressions (dynamic parts)
        const quasis = expr.quasis
        const expressions = expr.expressions

        // If there are no expressions, it's just a static string — separate rule
        if (expressions.length === 0) return

        context.report({
          node: expr,
          messageId: "usesCn",
          *fix(fixer) {
            const sourceCode = context.sourceCode || context.getSourceCode()
            const parts = []

            // First static part (trimmed)
            const firstStatic = quasis[0].value.raw.trim()
            if (firstStatic) {
              parts.push(JSON.stringify(firstStatic))
            }

            for (let i = 0; i < expressions.length; i++) {
              const exprNode = expressions[i]
              const exprText = sourceCode.getText(exprNode)
              const nextStatic = quasis[i + 1].value.raw.trim()

              // Handle different expression types
              if (exprNode.type === "ConditionalExpression") {
                // `${cond ? "a" : "b"}` → cond ? "a" : "b"
                // `${cond ? "a" : ""}` → cond && "a"
                const cons = exprNode.consequent
                const alt = exprNode.alternate
                const consText = sourceCode.getText(cons)
                const altText = sourceCode.getText(alt)
                const testText = sourceCode.getText(exprNode.test)

                const consIsEmpty = isEmptyString(cons, consText)
                const altIsEmpty = isEmptyString(alt, altText)

                if (altIsEmpty) {
                  // cond ? "classes" : "" → cond && "classes"
                  // Wrap compound tests (e.g. a || b) in parens — && binds tighter than ||
                  parts.push(`${wrapIfNeeded(testText, exprNode.test)} && ${consText}`)
                } else if (consIsEmpty) {
                  // cond ? "" : "classes" → !cond && "classes"
                  parts.push(`!${wrapIfNeeded(testText, exprNode.test)} && ${altText}`)
                } else {
                  parts.push(exprText)
                }
              } else if (exprNode.type === "LogicalExpression" && exprNode.operator === "??") {
                // `${className ?? ""}` → className
                const rightText = sourceCode.getText(exprNode.right)
                if (isEmptyString(exprNode.right, rightText)) {
                  parts.push(sourceCode.getText(exprNode.left))
                } else {
                  parts.push(exprText)
                }
              } else {
                parts.push(exprText)
              }

              if (nextStatic) {
                parts.push(JSON.stringify(nextStatic))
              }
            }

            const cnCall = `cn(${parts.join(", ")})`
            yield fixer.replaceText(expr, cnCall)
            yield* ensureCnImport(fixer, context)
          },
        })
      },
    }
  },
}

/**
 * Check if a node represents an empty string ("" or '').
 */
function isEmptyString(node, text) {
  return node.type === "Literal" && node.value === "" && (text === '""' || text === "''")
}

/**
 * Wrap an expression in parens if it contains operators that could cause
 * precedence issues when used with && or !.
 */
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
