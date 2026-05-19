/**
 * classnames/no-concatenation
 *
 * Disallow string concatenation (`+`) in className attributes. Use cn() instead.
 *
 * String concatenation has the same problems as template literals for Tailwind:
 *   - tailwind-merge cannot resolve class conflicts in concatenated strings
 *   - prettier-plugin-tailwindcss cannot sort classes across concat boundaries
 *   - Conditional logic is harder to read than separate cn() arguments
 *
 * ❌ className={"flex " + (active ? "bg-accent" : "text-muted")}
 * ✅ className={cn("flex", active ? "bg-accent" : "text-muted")}
 *
 * ❌ className={"base " + extraClasses}
 * ✅ className={cn("base", extraClasses)}
 */

import { ensureCnImport } from "../ensure-cn-import.js"

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description: "Disallow string concatenation in className; use cn() instead",
    },
    fixable: "code",
    messages: {
      useCn: "Use cn() instead of string concatenation for className.",
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

        // Look for BinaryExpression with "+" operator
        if (!isConcatExpression(expr)) return

        const sourceCode = context.sourceCode || context.getSourceCode()

        context.report({
          node: expr,
          messageId: "useCn",
          *fix(fixer) {
            const parts = flattenConcat(expr, sourceCode)
            yield fixer.replaceText(expr, `cn(${parts.join(", ")})`)
            yield* ensureCnImport(fixer, context)
          },
        })
      },
    }
  },
}

/**
 * Recursively check if a node is a string concatenation tree (all + operators).
 */
function isConcatExpression(node) {
  if (node.type !== "BinaryExpression" || node.operator !== "+") return false
  // At least one side must be a string-like value (literal or expression)
  return true
}

/**
 * Flatten a left-associative `+` chain into an array of cn() arguments.
 *
 * "flex " + (cond ? "a" : "b") + " px-2"
 *   → ["flex", cond ? "a" : "b", "px-2"]
 */
function flattenConcat(node, sourceCode) {
  const parts = []

  function walk(n) {
    if (n.type === "BinaryExpression" && n.operator === "+") {
      walk(n.left)
      walk(n.right)
    } else {
      parts.push(n)
    }
  }

  walk(node)

  return parts
    .map((part) => {
      if (part.type === "Literal" && typeof part.value === "string") {
        const trimmed = part.value.trim()
        if (!trimmed) return null // skip empty strings like "" or " "
        return JSON.stringify(trimmed)
      }

      if (part.type === "ConditionalExpression") {
        const testText = sourceCode.getText(part.test)
        const consText = sourceCode.getText(part.consequent)
        const altText = sourceCode.getText(part.alternate)
        const consIsEmpty = isEmptyString(part.consequent, consText)
        const altIsEmpty = isEmptyString(part.alternate, altText)

        if (altIsEmpty) {
          // Wrap compound tests (e.g. a || b) in parens — && binds tighter than ||
          return `${wrapIfNeeded(testText, part.test)} && ${consText}`
        } else if (consIsEmpty) {
          const wrappedTest = wrapIfNeeded(testText, part.test)
          return `!${wrappedTest} && ${altText}`
        }
        return sourceCode.getText(part)
      }

      // Variable, call expression, etc. — pass through
      return sourceCode.getText(part)
    })
    .filter(Boolean)
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
