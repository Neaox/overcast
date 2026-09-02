import { createSlugger } from "@/lib/slug"
import type { MdNode, MdParent } from "@/lib/remark-code-tabs"

/**
 * remark plugin: give every heading the id GitHub gives it, so a deep link
 * written against github.com or the public site lands in the docs viewer too.
 *
 * Ids are assigned here, on the Markdown tree, rather than in the h2/h3
 * renderers, for two reasons the renderers could not meet:
 *
 * - The text is the heading's full plain text, inline code included. A
 *   renderer only sees React children, and `String()` of a `<code>` child is
 *   "[object Object]" — which is what every CLI reference heading used to
 *   get as its id.
 * - Repeats are numbered in document order with one slugger per document
 *   ("setup", "setup-1", ...), the same numbering internal/docsindex derives
 *   for the "On this page" outline and the anchor checker.
 *
 * Runs BEFORE remark-code-tabs, which lifts the id off each `### Language`
 * heading it folds into a tab, so the tab's anchor is the heading's own.
 */
export function headingText(node: MdNode): string {
  // Astro's collector skips raw HTML in a heading; so does this.
  if (node.type === "html") return ""
  if (typeof node.value === "string") return node.value
  return (node.children ?? []).map(headingText).join("")
}

export default function remarkHeadingIds() {
  return (tree: MdParent) => {
    const nextId = createSlugger()
    const visit = (node: MdNode): void => {
      if (node.type === "heading") {
        const data = (node.data ??= {})
        const props = (data.hProperties ??= {}) as Record<string, unknown>
        if (typeof props.id !== "string") props.id = nextId(headingText(node))
        return
      }
      for (const child of node.children ?? []) visit(child)
    }
    visit(tree)
  }
}
