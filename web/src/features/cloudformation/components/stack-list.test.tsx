import { renderWithData, screen } from "@/test/render"
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
})
