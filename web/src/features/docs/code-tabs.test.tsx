import ReactMarkdown, { type Components } from "react-markdown"
import remarkRemoveComments from "remark-remove-comments"
import { beforeEach, describe, expect, it } from "vitest"
import { render, screen } from "@/test/render"
import remarkCodeTabs from "@/lib/remark-code-tabs"
import remarkHeadingIds from "@/lib/remark-heading-ids"
import { languageKey } from "./code-tab-language"
import { CodeTabsGroup, CodeTabsPanel } from "./code-tabs"

const components = {
  "code-tabs-group": CodeTabsGroup,
  "code-tabs-panel": CodeTabsPanel,
} as unknown as Components

function Doc({ markdown }: { markdown: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkHeadingIds, remarkCodeTabs, remarkRemoveComments]}
      components={components}
    >
      {markdown}
    </ReactMarkdown>
  )
}

const TWO_GROUPS = `
Intro.

<!-- BEGIN overcast:code-tabs -->

### Node.js (AWS SDK v3)

\`\`\`typescript
const first = "node"
\`\`\`

### Python (boto3)

\`\`\`python
first = "python"
\`\`\`

<!-- END overcast:code-tabs -->

Between the groups.

<!-- BEGIN overcast:code-tabs -->

### Node.js

second node panel

### Python

second python panel

<!-- END overcast:code-tabs -->
`

beforeEach(() => {
  localStorage.clear()
  window.location.hash = ""
})

describe("CodeTabsGroup", () => {
  it("renders one tablist per region and shows only the first tab's panel", () => {
    render(<Doc markdown={TWO_GROUPS} />)

    expect(screen.getAllByRole("tablist", { name: "Language" })).toHaveLength(2)
    expect(screen.getByText('const first = "node"')).toBeInTheDocument()
    expect(screen.queryByText('first = "python"')).not.toBeInTheDocument()
  })

  it("switches every group with a matching language and persists the pick", async () => {
    const { user } = render(<Doc markdown={TWO_GROUPS} />)

    await user.click(screen.getByRole("tab", { name: "Python (boto3)" }))

    expect(screen.getByText('first = "python"')).toBeInTheDocument()
    expect(screen.getByText("second python panel")).toBeInTheDocument()
    expect(screen.queryByText("second node panel")).not.toBeInTheDocument()
    expect(localStorage.getItem("overcast.docs.code-tab-language")).toBe("python")
  })

  it("restores the stored language on a fresh render, parenthetical or not", () => {
    localStorage.setItem("overcast.docs.code-tab-language", "python")

    render(<Doc markdown={TWO_GROUPS} />)

    expect(screen.getByText('first = "python"')).toBeInTheDocument()
    expect(screen.getByText("second python panel")).toBeInTheDocument()
    expect(screen.queryByText('const first = "node"')).not.toBeInTheDocument()
  })

  it("keeps each heading's anchor target alive and selects that tab for a deep link", () => {
    window.location.hash = "#python-boto3"

    const { container } = render(<Doc markdown={TWO_GROUPS} />)

    // GitHub's id for "### Node.js (AWS SDK v3)": the dot goes, no hyphen
    // replaces it.
    expect(container.querySelector("#nodejs-aws-sdk-v3")).not.toBeNull()
    expect(screen.getByText('first = "python"')).toBeInTheDocument()
  })

  it("degrades to plain headings when the plugin is absent (the GitHub rendering)", () => {
    render(<ReactMarkdown remarkPlugins={[remarkRemoveComments]}>{TWO_GROUPS}</ReactMarkdown>)

    expect(screen.queryByRole("tablist")).not.toBeInTheDocument()
    expect(
      screen.getByRole("heading", { level: 3, name: "Node.js (AWS SDK v3)" }),
    ).toBeInTheDocument()
    expect(screen.getByText("second node panel")).toBeInTheDocument()
    expect(screen.queryByText(/overcast:code-tabs/)).not.toBeInTheDocument()
  })
})

describe("languageKey", () => {
  it("drops a trailing parenthetical so labels across docs share a key", () => {
    expect(languageKey("Node.js (AWS SDK v3)")).toBe("nodejs")
    expect(languageKey("Node.js")).toBe("nodejs")
    expect(languageKey(".NET (AWS SDK)")).toBe("net")
  })
})
