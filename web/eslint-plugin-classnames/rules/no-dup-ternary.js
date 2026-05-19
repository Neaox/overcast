/**
 * classnames/no-dup-ternary
 *
 * When a cn() call contains a ternary where both branches share identical
 * Tailwind utility classes, hoist the shared classes out of the ternary.
 *
 * This keeps ternaries minimal — only the classes that actually change between
 * states should be conditional. Shared structural utilities (layout, gradient
 * direction, etc.) belong in a static cn() argument where tailwind-merge can
 * reason about them and prettier-plugin-tailwindcss can sort them.
 *
 * ❌ cn("base", cond
 *      ? "bg-linear-to-br from-teal-400/8 to-transparent"
 *      : "bg-linear-to-br from-teal-400/4 to-transparent")
 *
 * ✅ cn("base bg-linear-to-br to-transparent", cond
 *      ? "from-teal-400/8"
 *      : "from-teal-400/4")
 */

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description: "Hoist shared Tailwind classes out of cn() ternary branches",
    },
    fixable: "code",
    messages: {
      hoistShared:
        'Shared classes "{{shared}}" can be hoisted out of the ternary into a preceding cn() argument.',
    },
    schema: [],
  },

  create(context) {
    return {
      CallExpression(node) {
        if (!isCnCall(node)) return

        for (const arg of node.arguments) {
          if (arg.type !== "ConditionalExpression") continue

          const cons = arg.consequent
          const alt = arg.alternate

          // Both branches must be static strings
          if (cons.type !== "Literal" || typeof cons.value !== "string") continue
          if (alt.type !== "Literal" || typeof alt.value !== "string") continue

          const consClasses = cons.value.trim().split(/\s+/)
          const altClasses = alt.value.trim().split(/\s+/)

          // Find shared classes (exact match)
          const shared = consClasses.filter((c) => altClasses.includes(c))
          if (shared.length === 0) continue

          // Only auto-fix exact-match shared classes (safe). Classes that share
          // a Tailwind utility prefix but differ in value/modifier (e.g.
          // from-teal-400/8 vs from-teal-400/4) remain in the ternary — these
          // are the classes that genuinely vary between states.
          const sourceCode = context.sourceCode || context.getSourceCode()

          const remaining = (classes, sharedSet) => classes.filter((c) => !sharedSet.includes(c))
          const consRemaining = remaining(consClasses, shared)
          const altRemaining = remaining(altClasses, shared)

          context.report({
            node: arg,
            messageId: "hoistShared",
            data: { shared: shared.join(" ") },
            fix(fixer) {
              const argIndex = node.arguments.indexOf(arg)

              // Build the hoisted string
              const hoisted = shared.join(" ")

              // If one branch becomes empty after hoisting, simplify
              const consNew = consRemaining.join(" ")
              const altNew = altRemaining.join(" ")

              const fixes = []

              if (!consNew && !altNew) {
                // Both branches empty — replace entire ternary with the hoisted string
                fixes.push(fixer.replaceText(arg, JSON.stringify(hoisted)))
              } else {
                // Insert hoisted classes before the ternary
                // Find the preceding argument to append to, or insert a new one
                if (argIndex > 0) {
                  const prevArg = node.arguments[argIndex - 1]
                  if (prevArg.type === "Literal" && typeof prevArg.value === "string") {
                    // Append to preceding string
                    const newPrev = prevArg.value.trim() + " " + hoisted
                    fixes.push(fixer.replaceText(prevArg, JSON.stringify(newPrev)))
                  } else {
                    // Insert a new string argument before the ternary
                    fixes.push(fixer.insertTextBefore(arg, JSON.stringify(hoisted) + ", "))
                  }
                } else {
                  // No preceding argument — insert before
                  fixes.push(fixer.insertTextBefore(arg, JSON.stringify(hoisted) + ", "))
                }

                // Simplify the ternary
                if (!consNew && !altNew) {
                  // Remove the ternary entirely — already handled above
                } else if (!altNew) {
                  // cond ? "remaining" : "" → cond && "remaining"
                  const testText = sourceCode.getText(arg.test)
                  fixes.push(fixer.replaceText(arg, `${testText} && ${JSON.stringify(consNew)}`))
                } else if (!consNew) {
                  const testText = sourceCode.getText(arg.test)
                  const wrappedTest = wrapIfNeeded(testText, arg.test)
                  fixes.push(fixer.replaceText(arg, `!${wrappedTest} && ${JSON.stringify(altNew)}`))
                } else {
                  // Both branches still have remaining classes
                  const testText = sourceCode.getText(arg.test)
                  fixes.push(
                    fixer.replaceText(
                      arg,
                      `${testText} ? ${JSON.stringify(consNew)} : ${JSON.stringify(altNew)}`,
                    ),
                  )
                }
              }

              return fixes
            },
          })
        }
      },
    }
  },
}

function isCnCall(node) {
  return node.callee && node.callee.type === "Identifier" && node.callee.name === "cn"
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
