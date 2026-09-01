import { describe, expect, it } from "vitest"
import remarkCodeTabs, { type MdNode, type MdParent } from "./remark-code-tabs"

const BEGIN = "<!-- BEGIN overcast:code-tabs -->"
const END = "<!-- END overcast:code-tabs -->"

function html(value: string): MdNode {
  return { type: "html", value }
}

function h3(text: string): MdNode {
  return { type: "heading", depth: 3, children: [{ type: "text", value: text }] }
}

function code(value: string): MdNode {
  return { type: "code", lang: "typescript", value }
}

function para(text: string): MdNode {
  return { type: "paragraph", children: [{ type: "text", value: text }] }
}

function root(...children: MdNode[]): MdParent {
  return { type: "root", children }
}

function fold(tree: MdParent): MdParent {
  remarkCodeTabs()(tree)
  return tree
}

describe("remarkCodeTabs", () => {
  it("folds a sentinel region's h3 sections into one group with a panel per heading", () => {
    const tree = fold(
      root(
        para("intro"),
        html(BEGIN),
        h3("Node.js (AWS SDK v3)"),
        code("const a = 1"),
        h3("Python (boto3)"),
        code("a = 1"),
        html(END),
        para("outro"),
      ),
    )

    expect(tree.children.map((n) => n.type)).toEqual(["paragraph", "codeTabsGroup", "paragraph"])
    const group = tree.children[1]
    expect(group.data).toEqual({ hName: "code-tabs-group" })
    expect(group.children).toHaveLength(2)
    expect(group.children![0].data).toEqual({
      hName: "code-tabs-panel",
      hProperties: { dataLabel: "Node.js (AWS SDK v3)", dataTabId: "node-js-aws-sdk-v3" },
    })
    expect(group.children![0].children).toEqual([code("const a = 1")])
    expect(group.children![1].data!.hProperties).toEqual({
      dataLabel: "Python (boto3)",
      dataTabId: "python-boto3",
    })
  })

  it("hoists content before the first heading out above the group", () => {
    const tree = fold(root(html(BEGIN), para("lead-in"), h3("Go"), code("x"), html(END)))

    expect(tree.children.map((n) => n.type)).toEqual(["paragraph", "codeTabsGroup"])
    expect(tree.children[1].children).toHaveLength(1)
  })

  it("drops --- separators inside the region, which the tab chrome replaces", () => {
    const tree = fold(
      root(
        html(BEGIN),
        h3("Go"),
        code("x"),
        { type: "thematicBreak" },
        h3("Java"),
        code("y"),
        html(END),
      ),
    )

    const group = tree.children[0]
    expect(group.children).toHaveLength(2)
    expect(group.children![0].children).toEqual([code("x")])
  })

  it("keeps a panel's deeper headings and prose inside that panel", () => {
    const tree = fold(
      root(
        html(BEGIN),
        h3("Node.js"),
        code("x"),
        { type: "heading", depth: 4, children: [{ type: "text", value: "Variant" }] },
        para("more"),
        html(END),
      ),
    )

    expect(tree.children[0].children![0].children!.map((n) => n.type)).toEqual([
      "code",
      "heading",
      "paragraph",
    ])
  })

  it("leaves an unmatched BEGIN alone for remark-remove-comments to strip", () => {
    const before = root(html(BEGIN), h3("Node.js"), code("x"))
    const tree = fold(root(html(BEGIN), h3("Node.js"), code("x")))

    expect(tree.children).toEqual(before.children)
  })

  it("keeps a headingless region's content and drops only the sentinels", () => {
    const tree = fold(root(html(BEGIN), para("just prose"), html(END)))

    expect(tree.children).toEqual([para("just prose")])
  })

  it("folds each of several regions independently", () => {
    const tree = fold(
      root(
        html(BEGIN),
        h3("Node.js"),
        code("one"),
        html(END),
        para("between"),
        html(BEGIN),
        h3("Python"),
        code("two"),
        html(END),
      ),
    )

    expect(tree.children.map((n) => n.type)).toEqual([
      "codeTabsGroup",
      "paragraph",
      "codeTabsGroup",
    ])
  })
})
