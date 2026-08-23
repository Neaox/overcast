/**
 * The bucket list on `ResourceTable`: the header sort, and the one piece of
 * behaviour the conversion had to re-establish by hand — the copy-URL control
 * lives in a row that navigates on click, so opening its menu must not open
 * the bucket.
 */
import { renderWithData, screen, within } from "@/test/render"
import { s3BucketsQueryOptions } from "@/features/s3/data"
import { BucketList } from "./bucket-list"

const nav = vi.hoisted(() => ({ navigate: vi.fn() }))

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => nav.navigate,
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => <button type="button">Docs</button>,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/features/debug/raw-state-link", () => ({
  RawStateLink: () => null,
}))

vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: vi.fn(), isPending: false }),
}))

const buckets = [
  { name: "uploads", creationDate: "2026-05-02T00:00:00Z" },
  { name: "artifacts", creationDate: "2026-01-11T00:00:00Z" },
  { name: "logs", creationDate: "2026-08-19T00:00:00Z" },
]

function rowOrder(): (string | null)[] {
  const [, body] = screen.getAllByRole("rowgroup")
  return within(body)
    .getAllByRole("row")
    .map((row) => within(row).getAllByRole("cell")[0].textContent)
}

beforeEach(() => nav.navigate.mockClear())

describe("BucketList", () => {
  it("renders the buckets in the order ListBuckets returned them", () => {
    renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, buckets]])

    expect(screen.getByRole("heading", { name: "S3 Buckets" })).toBeInTheDocument()
    expect(rowOrder()).toEqual(["uploads", "artifacts", "logs"])
  })

  it("sorts by name on the Name header, and reverses on a second click", async () => {
    const { user } = renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, buckets]])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["artifacts", "logs", "uploads"])

    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(rowOrder()).toEqual(["uploads", "logs", "artifacts"])
  })

  it("sorts newest-first on the Created header", async () => {
    const { user } = renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, buckets]])

    await user.click(screen.getByRole("button", { name: "Created" }))
    expect(rowOrder()).toEqual(["logs", "uploads", "artifacts"])
  })

  it("navigates to the bucket when the row is clicked", async () => {
    const { user } = renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, buckets]])

    const [, body] = screen.getAllByRole("rowgroup")
    await user.click(within(body).getAllByRole("row")[0])

    expect(nav.navigate).toHaveBeenCalledWith({
      to: "/s3/$bucket",
      params: { bucket: "uploads" },
    })
  })

  it("does not navigate when the copy-URL control is clicked", async () => {
    const { user } = renderWithData(<BucketList />, [[s3BucketsQueryOptions().queryKey, buckets]])

    await user.click(screen.getAllByTitle("Copy URL")[0])

    expect(nav.navigate).not.toHaveBeenCalled()
  })
})
