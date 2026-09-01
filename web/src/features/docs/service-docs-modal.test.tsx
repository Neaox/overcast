/**
 * Link resolution for the service docs modal, and the two behaviours the
 * split into sub-pages depends on: a link to docs/services/<key>/operations.md
 * loads in the modal rather than escaping to a broken relative URL, and
 * GitHub alert syntax renders as a callout rather than a literal "[!NOTE]".
 */
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { classifyDocLink, docTitle, resolveDocPath } from "./service-doc-links"
import { ServiceDocsModal } from "./service-docs-modal"

describe("resolveDocPath", () => {
  it.each([
    // From a landing page.
    ["services/s3.md", "s3/operations.md", "services/s3/operations.md"],
    ["services/s3.md", "./s3/operations.md", "services/s3/operations.md"],
    ["services/s3.md", "lambda.md", "services/lambda.md"],
    ["services/s3.md", "../configuration.md", "configuration.md"],
    // From a sub-page: "../" has to walk out of the service directory.
    ["services/s3/operations.md", "../s3.md", "services/s3.md"],
    ["services/s3/operations.md", "limitations.md", "services/s3/limitations.md"],
    ["services/s3/operations.md", "../../cdk.md", "cdk.md"],
    // Fragments are dropped; the modal has no in-page anchor navigation.
    ["services/s3.md", "s3/operations.md#summary", "services/s3/operations.md"],
  ])("resolves %s + %s to %s", (from, href, want) => {
    expect(resolveDocPath(from, href)).toBe(want)
  })

  it.each([
    ["services/s3.md", "https://docs.aws.amazon.com/x.html"],
    ["services/s3.md", "mailto:x@example.com"],
    ["services/s3.md", "#summary"],
    // Walking above the docs root is not a doc this modal can serve.
    ["services/s3.md", "../../../etc/passwd"],
  ])("returns null for %s + %s", (from, href) => {
    expect(resolveDocPath(from, href)).toBeNull()
  })
})

describe("classifyDocLink", () => {
  it("hands a service with a console route to the app", () => {
    // Given / When: a cross-service link from one landing page to another
    const link = classifyDocLink("services/s3.md", "lambda.md")

    // Then: the reader gets Lambda's console page with its docs open, which is
    // what they almost always wanted
    expect(link).toEqual({ kind: "route", href: "/lambda#docs" })
  })

  it("loads a sub-page in the modal", () => {
    // Given / When: the Operations stub's link to the generated table
    const link = classifyDocLink("services/s3.md", "s3/operations.md")

    // Then: it stays in the modal
    expect(link).toEqual({ kind: "doc", path: "services/s3/operations.md", label: "s3 operations" })
  })

  it("loads a service with no console route in the modal", () => {
    // Given / When: Athena has docs but no console page
    const link = classifyDocLink("services/s3.md", "athena.md")

    // Then: showing the page beats opening a dead relative URL in a new tab
    expect(link).toEqual({ kind: "doc", path: "services/athena.md", label: "athena" })
  })

  it("treats anything outside docs/services as an outbound link", () => {
    expect(classifyDocLink("services/s3.md", "../configuration.md").kind).toBe("external")
    expect(classifyDocLink("services/s3.md", "https://aws.amazon.com").kind).toBe("external")
  })
})

describe("docTitle", () => {
  it("names a sub-page by its service's display name", () => {
    expect(docTitle("services/cloudwatch-logs/operations.md", "CloudWatch Logs")).toBe(
      "CloudWatch Logs operations",
    )
  })
})

function renderModal() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ServiceDocsModal service="s3" label="S3" open onClose={() => {}} />
    </QueryClientProvider>,
  )
}

describe("ServiceDocsModal", () => {
  const pages: Record<string, string> = {
    "services/s3.md": [
      "# S3",
      "",
      "> [!WARNING]",
      "> Buckets are not versioned by default.",
      "",
      "## Operations",
      "",
      "45 of 53 listed operations are implemented.",
      "Per-operation status, notes and AWS API links: [S3 operations](s3/operations.md).",
    ].join("\n"),
    "services/s3/operations.md": "# S3 operations\n\nBack to [S3](../s3.md).\n\n## Summary\n",
  }

  beforeEach(() => {
    vi.stubGlobal("fetch", (input: string) => {
      const path = decodeURIComponent(
        new URL(input, "http://localhost").searchParams.get("path") || "",
      )
      return Promise.resolve(
        Object.hasOwn(pages, path)
          ? new Response(pages[path], { status: 200 })
          : new Response("not found", { status: 404 }),
      )
    })
  })

  afterEach(() => vi.unstubAllGlobals())

  it("follows a sub-page link in place and offers a way back", async () => {
    // Given: the landing page, showing the generated Operations stub
    renderModal()
    const link = await screen.findByRole("button", { name: "S3 operations" })

    // When: the reader follows it
    await userEvent.click(link)

    // Then: the sub-page loads in the modal, not in a new tab
    await screen.findByRole("heading", { level: 1, name: "S3 operations" })

    // And: the link back returns to the landing page
    await userEvent.click(screen.getByRole("button", { name: "S3" }))
    await screen.findByRole("heading", { level: 1, name: "S3" })
  })

  it("renders GitHub alert syntax as a callout, not a literal [!WARNING]", async () => {
    // Given / When: a page using the alert syntax the template prescribes.
    // The dialog portals out of the render container, so assert against the
    // document.
    renderModal()

    // Then: it becomes a styled callout carrying the alert's own title colour,
    // and the marker never reaches the page as text
    await screen.findByRole("heading", { level: 1, name: "S3" })
    const callout = document.body.querySelector('[style*="--alert-title-color"]')
    expect(callout).toBeInTheDocument()
    expect(callout?.textContent).toContain("Warning")
    expect(document.body.textContent).not.toContain("[!WARNING]")
  })
})
