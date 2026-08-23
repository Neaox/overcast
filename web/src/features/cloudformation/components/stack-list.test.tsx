import { http, HttpResponse } from "msw"
import { within } from "@testing-library/react"
import { renderWithData, screen, waitFor } from "@/test/render"
import { server } from "@/test/server"
import { cfnStacksQueryOptions } from "@/features/cloudformation/data"
import { StackList } from "./stack-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => null,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("./create-stack-dialog", () => ({
  CreateStackDialog: () => null,
}))

function seed(stacks: Record<string, unknown>[]) {
  return [[cfnStacksQueryOptions().queryKey, stacks]] as [readonly unknown[], unknown][]
}

describe("StackList", () => {
  it("shows why a failed stack failed, from the reason ListStacks returns", () => {
    renderWithData(
      <StackList />,
      seed([
        {
          StackName: "orders-api",
          StackStatus: "UPDATE_ROLLBACK_FAILED",
          StackStatusReason: "rollback failed: bucket not empty",
        },
      ]),
    )

    expect(screen.getByText("rollback failed: bucket not empty")).toBeInTheDocument()
  })

  // ROLLBACK_COMPLETE clears StackStatusReason, so the row falls back to what
  // the status itself means — which is the part a user is most likely not to
  // know: this stack can never be updated, only deleted.
  it("falls back to the status's meaning when there is no reason", () => {
    renderWithData(
      <StackList />,
      seed([{ StackName: "orders-api", StackStatus: "ROLLBACK_COMPLETE" }]),
    )

    expect(screen.getByText(/can only be deleted/i)).toBeInTheDocument()
  })

  it("adds no note to a healthy stack", () => {
    renderWithData(
      <StackList />,
      seed([{ StackName: "orders-api", StackStatus: "CREATE_COMPLETE" }]),
    )

    expect(screen.getByText("Create Complete")).toBeInTheDocument()
    expect(screen.queryByText(/rolled back/i)).not.toBeInTheDocument()
  })

  // An empty stack list is what a region mismatch looks like: the deploy went
  // somewhere, and this page is the only place the developer is looking.
  it("explains an empty list when the stacks are in another region", async () => {
    server.use(
      http.get("/api/preflight/region", () =>
        HttpResponse.json({
          kind: "cloudformation-stacks",
          region: "us-east-1",
          count: 0,
          elsewhere: [{ region: "ap-southeast-2", count: 3 }],
        }),
      ),
    )

    renderWithData(<StackList />, seed([]))

    expect(await screen.findByText(/No stacks in/)).toHaveTextContent(
      "No stacks in us-east-1. There are 3 in ap-southeast-2.",
    )
  })

  // The default handler answers "nothing anywhere", which is what an empty
  // account looks like — and an empty account must produce nothing at all.
  it("says nothing about regions when there is nothing anywhere", async () => {
    let asked = 0
    server.use(
      http.get("/api/preflight/region", () => {
        asked++
        return HttpResponse.json({
          kind: "cloudformation-stacks",
          region: "us-east-1",
          count: 0,
          elsewhere: [],
        })
      }),
    )

    renderWithData(<StackList />, seed([]))

    expect(await screen.findByText("No stacks yet")).toBeInTheDocument()
    await waitFor(() => expect(asked).toBe(1))
    expect(screen.queryByText(/No stacks in/)).not.toBeInTheDocument()
  })

  // A page that rendered rows has no symptom to explain, so the check is never
  // even asked — the notice is mounted only inside the empty branch.
  it("does no cross-region work when the list is not empty", () => {
    let asked = 0
    server.use(
      http.get("/api/preflight/region", () => {
        asked++
        return HttpResponse.json({
          kind: "cloudformation-stacks",
          region: "us-east-1",
          count: 0,
          elsewhere: [{ region: "ap-southeast-2", count: 3 }],
        })
      }),
    )

    renderWithData(
      <StackList />,
      seed([{ StackName: "orders-api", StackStatus: "CREATE_COMPLETE" }]),
    )

    expect(asked).toBe(0)
  })
})

// The list is on `ResourceTable`'s TanStack row model since #1327 wave B, so
// sorting is the engine's rather than a per-page `useState`. These pin the two
// halves a caller has to get right: the header actually reorders rows, and the
// column's explicit id — the token that ends up in `?sort=` — is what the page
// is told about.
describe("StackList — sorting", () => {
  const unordered = seed([
    { StackName: "zulu-api", StackStatus: "CREATE_COMPLETE" },
    { StackName: "alpha-api", StackStatus: "CREATE_COMPLETE" },
    { StackName: "mike-api", StackStatus: "CREATE_COMPLETE" },
  ])

  const stackNames = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((row) => within(row).getAllByRole("cell")[0].textContent)

  it("reorders the rows on a header click, and reverses on the second", async () => {
    const { user } = renderWithData(<StackList />, unordered)

    expect(stackNames()).toEqual(["zulu-api", "alpha-api", "mike-api"])

    await user.click(screen.getByRole("button", { name: "Stack name" }))
    expect(stackNames()).toEqual(["alpha-api", "mike-api", "zulu-api"])
    expect(screen.getByRole("columnheader", { name: /stack name/i })).toHaveAttribute(
      "aria-sort",
      "ascending",
    )

    await user.click(screen.getByRole("button", { name: "Stack name" }))
    expect(stackNames()).toEqual(["zulu-api", "mike-api", "alpha-api"])
  })

  it("hands the route the column's own sort id, not a slug of the header", async () => {
    const onSortChange = vi.fn()
    const { user } = renderWithData(
      <StackList sort={undefined} onSortChange={onSortChange} />,
      unordered,
    )

    await user.click(screen.getByRole("button", { name: "Stack name" }))

    expect(onSortChange).toHaveBeenCalledWith({ id: "name", desc: false })
  })

  it("orders by the sort the route supplies, before any click", () => {
    renderWithData(
      <StackList sort={{ id: "name", desc: true }} onSortChange={vi.fn()} />,
      unordered,
    )

    expect(stackNames()).toEqual(["zulu-api", "mike-api", "alpha-api"])
  })
})
