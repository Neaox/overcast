import { render, screen, userEvent } from "@/test/render"
import type { S3ObjectVersion } from "@/types"
import { ObjectVersionList } from "./object-version-list"

function version(versionId: string, over: Partial<S3ObjectVersion> = {}): S3ObjectVersion {
  return {
    key: "logs/app.log",
    versionId,
    isLatest: false,
    isDeleteMarker: false,
    lastModified: "2026-01-01T00:00:00.000Z",
    size: 1024,
    etag: "abc",
    storageClass: "STANDARD",
    ...over,
  }
}

const history = [version("v3", { isLatest: true }), version("v2"), version("v1")]

function renderList(props: Partial<Parameters<typeof ObjectVersionList>[0]> = {}) {
  return render(
    <ObjectVersionList
      versions={history}
      isTruncated={false}
      loading={false}
      onSelect={() => {}}
      {...props}
    />,
  )
}

/** The row the list is pointing at, by its (shortened) version id. */
function currentRow(): string | undefined {
  return screen
    .getAllByRole("button")
    .find((b) => b.getAttribute("aria-current") === "true")
    ?.textContent?.replace(/\s+/g, " ")
    .trim()
}

describe("ObjectVersionList", () => {
  it("marks the revision the inspector is showing", () => {
    renderList({ selectedVersionId: "v2" })
    expect(currentRow()).toContain("v2")
  })

  it("marks the current revision when the inspector was opened on the key", () => {
    // No version id means the inspector followed the key, which resolves to
    // whichever revision is latest — so that is the row that is being read.
    renderList()
    expect(currentRow()).toContain("v3")
  })

  it("hands back the revision that was clicked", async () => {
    const onSelect = vi.fn()
    renderList({ onSelect })
    const user = userEvent.setup()

    await user.click(screen.getByText("v1"))
    expect(onSelect).toHaveBeenCalledWith("v1")
  })

  it("draws a delete marker as a tombstone rather than a zero-byte object", () => {
    // Without the distinction a tombstone and an object that was never written
    // look identical, which is the question the history exists to answer.
    renderList({ versions: [version("v4", { isLatest: true, isDeleteMarker: true, size: 0 })] })

    expect(screen.getByText("Delete marker")).toBeInTheDocument()
    expect(screen.queryByText("Current")).not.toBeInTheDocument()
    expect(screen.getByText("—")).toBeInTheDocument()
  })

  it("offers a delete marker as a destination — the details pane explains it", () => {
    const onSelect = vi.fn()
    renderList({
      versions: [version("v4", { isLatest: true, isDeleteMarker: true })],
      onSelect,
    })
    expect(screen.getByRole("button")).toBeEnabled()
  })

  it("says when the history was capped rather than letting it read as complete", () => {
    renderList({ isTruncated: true })
    expect(screen.getByText(/this object has more/i)).toBeInTheDocument()
  })

  it("stays quiet about a cap that did not happen", () => {
    renderList()
    expect(screen.queryByText(/this object has more/i)).not.toBeInTheDocument()
  })

  it("explains an empty history instead of showing an empty box", () => {
    renderList({ versions: [] })
    expect(screen.getByText(/no stored revisions/i)).toBeInTheDocument()
  })
})
