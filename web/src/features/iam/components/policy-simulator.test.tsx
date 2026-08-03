import { renderWithData, screen } from "@/test/render"
import { serverInfoQueryOptions } from "@/hooks/use-server-info"
import { iamUsersQueryOptions, iamRolesQueryOptions } from "@/features/iam/data"
import { PolicySimulator, EnforcementNotice } from "./policy-simulator"

describe("EnforcementNotice", () => {
  it("says enforcement is off by default", () => {
    renderWithData(<EnforcementNotice />, [[serverInfoQueryOptions().queryKey, { iam_enforce: false }]])

    expect(screen.getByTestId("iam-enforcement-notice")).toHaveTextContent(
      "IAM enforcement is OFF (the default).",
    )
  })

  it("warns when enforcement is on so a denial is not mistaken for an app bug", () => {
    renderWithData(<EnforcementNotice />, [[serverInfoQueryOptions().queryKey, { iam_enforce: true }]])

    const notice = screen.getByTestId("iam-enforcement-notice")
    expect(notice).toHaveTextContent("IAM enforcement is ON.")
    expect(notice).toHaveTextContent("AccessDenied")
  })
})

describe("PolicySimulator", () => {
  it("offers the stored principals and starts with no results", () => {
    renderWithData(<PolicySimulator />, [
      [iamUsersQueryOptions().queryKey, [{ UserName: "alice", Arn: "arn:aws:iam::000000000000:user/alice" }]],
      [iamRolesQueryOptions().queryKey, [{ RoleName: "task", Arn: "arn:aws:iam::000000000000:role/task" }]],
      [serverInfoQueryOptions().queryKey, { iam_enforce: false }],
    ])

    expect(screen.getByRole("option", { name: "user/alice" })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: "role/task" })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /simulate/i })).toBeInTheDocument()
    expect(screen.getByText("No simulation yet")).toBeInTheDocument()
  })
})
