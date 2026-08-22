/**
 * classnames/no-raw-spinner-in-content
 *
 * docs/plans/web-ui-dry-refactor.md §7: "flag `<Spinner>` that is not inside
 * a `<Button>`, `<Badge>` or toast. Encodes the 5b rule ('14-16px, chips and
 * toasts only') that today lives only in a docstring."
 *
 * `Spinner`'s own doc comment (components/ui/primitives.tsx) says the design
 * system allows it "at 14-16px only, and only inside a chip, button or
 * toast — a content area gets a skeleton instead (SkeletonRows /
 * SkeletonCards)". A bare `<Spinner>` centred in a page or panel is exactly
 * the content-area case the rule exists to catch.
 *
 * ❌ if (isLoading) return <div className="flex justify-center py-32"><Spinner className="h-6 w-6" /></div>
 * ✅ if (isLoading) return <SkeletonRows />
 */

const ALLOWED_JSX_ANCESTORS = new Set(["Button", "Badge"])

function jsxElementName(node) {
  return node.openingElement.name.type === "JSXIdentifier" ? node.openingElement.name.name : null
}

function calleeName(node) {
  if (node.callee.type === "Identifier") return node.callee.name
  if (node.callee.type === "MemberExpression" && node.callee.object.type === "Identifier") {
    return node.callee.object.name
  }
  return null
}

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Disallow <Spinner> outside a <Button>, <Badge>, or toast() call — a content area should render a skeleton instead",
    },
    schema: [],
    messages: {
      noRawSpinnerInContent:
        "<Spinner> belongs inside a <Button>, <Badge>, or toast — a content area should render <SkeletonRows>/<SkeletonCards> instead.",
    },
  },

  create(context) {
    return {
      JSXElement(node) {
        const name = node.openingElement.name
        if (name.type !== "JSXIdentifier" || name.name !== "Spinner") return

        let allowed = false
        for (let parent = node.parent; parent; parent = parent.parent) {
          if (parent.type === "JSXElement" && ALLOWED_JSX_ANCESTORS.has(jsxElementName(parent))) {
            allowed = true
            break
          }
          if (parent.type === "CallExpression" && calleeName(parent) === "toast") {
            allowed = true
            break
          }
          // Don't climb out of the current component — a Spinner passed as a
          // prop/children into an unrelated call is out of scope for this rule.
          if (parent.type === "FunctionDeclaration" || parent.type === "ArrowFunctionExpression")
            break
        }

        if (!allowed) {
          context.report({ node: node.openingElement, messageId: "noRawSpinnerInContent" })
        }
      },
    }
  },
}
