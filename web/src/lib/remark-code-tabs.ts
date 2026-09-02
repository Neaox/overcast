import { slug } from "@/lib/slug"

/**
 * remark plugin: fold a sentinel-delimited run of `### Language` sections into
 * one node the docs viewer renders as tabs (see features/docs/code-tabs.tsx).
 *
 * Authoring convention, in published docs:
 *
 *     <!-- BEGIN overcast:code-tabs -->
 *
 *     ### Node.js
 *
 *     ```typescript
 *     ...
 *     ```
 *
 *     ### Python
 *
 *     ...
 *
 *     <!-- END overcast:code-tabs -->
 *
 * The sentinels are HTML comments, so on GitHub — where the same files are
 * read — the region degrades to plain headings and fenced blocks. Inside the
 * viewer, every depth-3 heading starts a tab whose label is the heading text
 * and whose panel is everything up to the next depth-3 heading; `---`
 * separators between languages (kept for the GitHub rendering) are dropped,
 * since the tab chrome replaces them. Content before the first heading is
 * hoisted out above the tab group. Deeper headings (h4+) stay inside their
 * tab's panel.
 *
 * The headings deliberately still exist in the raw Markdown, so
 * internal/docsindex keeps indexing them for search, the "On this page"
 * nav, and anchor checking; the tab group re-establishes each heading's
 * anchor id — the one remark-heading-ids assigned it, or the slug of the
 * label when that plugin did not run — so those deep links stay live.
 *
 * Ordering: this plugin must run AFTER remark-heading-ids, so the folded
 * headings carry their ids, and BEFORE remark-remove-comments, which would
 * otherwise delete the sentinels. An unmatched BEGIN is left alone — the
 * comment is stripped downstream and the content renders as plain headings,
 * the GitHub degradation.
 */

const BEGIN_RE = /^<!--\s*BEGIN overcast:code-tabs\s*-->$/
const END_RE = /^<!--\s*END overcast:code-tabs\s*-->$/

// Minimal structural mdast types: @types/mdast is not a direct dependency of
// this package, and with pnpm's no-hoist install reaching into react-markdown's
// transitive copy is not an option.
export interface MdNode {
  type: string
  value?: unknown
  depth?: unknown
  data?: Record<string, unknown>
  children?: MdNode[]
  [key: string]: unknown
}

export interface MdParent extends MdNode {
  children: MdNode[]
}

function isSentinel(node: MdNode, re: RegExp): boolean {
  return node.type === "html" && typeof node.value === "string" && re.test(node.value.trim())
}

/** Concatenated plain text of a node's inline children (tab label source). */
function textOf(node: MdNode): string {
  if (typeof node.value === "string") return node.value
  return (node.children ?? []).map(textOf).join("")
}

interface Tab {
  label: string
  /** The heading's anchor id, which the panel keeps alive. */
  id: string
  children: MdNode[]
}

/** The id remark-heading-ids gave a heading, if it ran. */
function headingId(node: MdNode): string | undefined {
  const id = (node.data?.hProperties as Record<string, unknown> | undefined)?.id
  return typeof id === "string" ? id : undefined
}

function groupNode(tabs: Tab[]): MdNode {
  return {
    type: "codeTabsGroup",
    data: { hName: "code-tabs-group" },
    children: tabs.map((tab): MdNode => ({
      type: "codeTabsPanel",
      data: {
        hName: "code-tabs-panel",
        hProperties: { dataLabel: tab.label, dataTabId: tab.id },
      },
      children: tab.children,
    })),
  }
}

/** Fold one sentinel region's nodes; returns what replaces region + sentinels. */
function fold(region: MdNode[]): MdNode[] {
  const lead: MdNode[] = []
  const tabs: Tab[] = []
  for (const node of region) {
    if (node.type === "heading" && node.depth === 3) {
      const label = textOf(node).trim()
      tabs.push({ label, id: headingId(node) ?? slug(label), children: [] })
      continue
    }
    if (node.type === "thematicBreak") continue
    ;(tabs.length > 0 ? tabs[tabs.length - 1].children : lead).push(node)
  }
  // A region with no headings has nothing to tab: keep its content, lose the
  // sentinels.
  if (tabs.length === 0) return region
  return [...lead, groupNode(tabs)]
}

export default function remarkCodeTabs() {
  return (tree: MdParent) => {
    const children = tree.children
    const out: MdNode[] = []
    let i = 0
    while (i < children.length) {
      const node = children[i]
      if (!isSentinel(node, BEGIN_RE)) {
        out.push(node)
        i++
        continue
      }
      let end = -1
      for (let j = i + 1; j < children.length; j++) {
        if (isSentinel(children[j], END_RE)) {
          end = j
          break
        }
      }
      if (end < 0) {
        out.push(node)
        i++
        continue
      }
      out.push(...fold(children.slice(i + 1, end)))
      i = end + 1
    }
    tree.children = out
  }
}
