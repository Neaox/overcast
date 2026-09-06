/**
 * Shared helper: yields an additional fixer operation to add
 * `import { cn } from "@/lib/utils"` when the file doesn't already have it.
 *
 * Usage inside a rule's fix *generator*:
 *
 *   *fix(fixer) {
 *     yield fixer.replaceText(node, newText)
 *     yield* ensureCnImport(fixer, context)
 *   }
 */
export function* ensureCnImport(fixer, context) {
  const sourceCode = context.sourceCode || context.getSourceCode()
  const body = sourceCode.ast.body

  // Walk ImportDeclarations to check if cn is already imported
  let lastImport = null
  for (const node of body) {
    if (node.type !== "ImportDeclaration") continue
    lastImport = node

    // Check if this import already brings in `cn` from "@/lib/utils"
    if (
      node.source.value === "@/lib/utils" &&
      node.specifiers.some((s) => s.type === "ImportSpecifier" && s.imported.name === "cn")
    ) {
      return // already imported
    }
  }

  const importStatement = 'import { cn } from "@/lib/utils"'

  if (lastImport) {
    yield fixer.insertTextAfter(lastImport, "\n" + importStatement)
  } else {
    yield fixer.insertTextBeforeRange([0, 0], importStatement + "\n")
  }
}
