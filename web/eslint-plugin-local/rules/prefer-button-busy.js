/**
 * local/prefer-button-busy
 *
 * docs/plans/web-ui-dry-refactor.md §7: "flag a JSX `<Button>` whose
 * `disabled` expression mentions `isPending` or whose children contain
 * `<Spinner>`. Enforces P4."
 *
 * `Button.busy` (components/ui/button.tsx) already encodes "disabled while a
 * mutation is pending, with a spinner in place of the label" as a single
 * prop. 120+ call sites re-derive that by hand instead — `disabled={…
 * isPending}` plus a `{…isPending && <Spinner …>}` child — which is exactly
 * the abstraction-bypass the audit's thesis calls out.
 *
 * ❌ <Button disabled={isPending}>{isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}Create</Button>
 * ✅ <Button busy={isPending}>Create</Button>
 */

const IS_PENDING = /isPending/

/** @type {import('eslint').Rule.RuleModule} */
export default {
  meta: {
    type: "suggestion",
    docs: {
      description:
        "Prefer <Button busy={…}> over hand-rolling disabled={…isPending} plus a <Spinner> child",
    },
    schema: [],
    messages: {
      preferBusy:
        "This <Button> hand-rolls the busy state — use the busy prop (<Button busy={…isPending}>) instead of disabled={…isPending} plus a <Spinner> child.",
    },
  },

  create(context) {
    const sourceCode = context.sourceCode ?? context.getSourceCode()

    function disabledMentionsIsPending(openingElement) {
      const attr = openingElement.attributes.find(
        (a) => a.type === "JSXAttribute" && a.name.name === "disabled",
      )
      if (!attr || !attr.value || attr.value.type !== "JSXExpressionContainer") return false
      return IS_PENDING.test(sourceCode.getText(attr.value.expression))
    }

    function childrenContainSpinner(element) {
      return element.children.some(
        (child) =>
          child.type === "JSXElement" &&
          child.openingElement.name.type === "JSXIdentifier" &&
          child.openingElement.name.name === "Spinner",
      )
    }

    return {
      JSXElement(node) {
        const name = node.openingElement.name
        if (name.type !== "JSXIdentifier" || name.name !== "Button") return

        if (disabledMentionsIsPending(node.openingElement) || childrenContainSpinner(node)) {
          context.report({ node: node.openingElement, messageId: "preferBusy" })
        }
      },
    }
  },
}
