import { describe, expect, it } from "vitest"
import remarkHeadingIds, { headingText } from "./remark-heading-ids"
import type { MdNode, MdParent } from "./remark-code-tabs"

function heading(depth: number, ...children: MdNode[]): MdNode {
  return { type: "heading", depth, children }
}

function text(value: string): MdNode {
  return { type: "text", value }
}

function ids(tree: MdParent): unknown[] {
  return tree.children
    .filter((n) => n.type === "heading")
    .map((n) => ((n.data?.hProperties ?? {}) as Record<string, unknown>).id)
}

describe("remarkHeadingIds", () => {
  it("ids a heading by its full plain text, inline code included", () => {
    const tree: MdParent = {
      type: "root",
      children: [
        heading(3, { type: "inlineCode", value: "overcast stop [name]" }),
        heading(2, text("Stack stuck in "), { type: "inlineCode", value: "CREATE_IN_PROGRESS" }),
        heading(2, text("Data-plane endpoints — RDS, and anything else that is a container")),
      ],
    }

    remarkHeadingIds()(tree)

    expect(ids(tree)).toEqual([
      "overcast-stop-name",
      "stack-stuck-in-create_in_progress",
      "data-plane-endpoints--rds-and-anything-else-that-is-a-container",
    ])
  })

  it("numbers repeats in document order the way GitHub does", () => {
    const tree: MdParent = {
      type: "root",
      children: [
        heading(2, text("Setup")),
        { type: "paragraph", children: [text("prose")] },
        heading(2, text("Setup")),
        heading(3, text("Setup-1")),
        heading(2, text("Setup")),
      ],
    }

    remarkHeadingIds()(tree)

    expect(ids(tree)).toEqual(["setup", "setup-1", "setup-1-1", "setup-2"])
  })

  it("keeps an id a heading already carries", () => {
    const tree: MdParent = {
      type: "root",
      children: [{ ...heading(2, text("Setup")), data: { hProperties: { id: "custom" } } }],
    }

    remarkHeadingIds()(tree)

    expect(ids(tree)).toEqual(["custom"])
  })

  it("reads text through emphasis and links but not raw HTML", () => {
    const node = heading(
      2,
      { type: "strong", children: [text("Bold")] },
      text(" "),
      { type: "link", url: "x", children: [text("linked")] },
      { type: "html", value: "<br>" },
    )

    expect(headingText(node)).toBe("Bold linked")
  })
})
