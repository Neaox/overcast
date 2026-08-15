import type { ReactNode } from "react"
import { renderWithData, screen, within } from "@/test/render"
import {
  cfnEventsInfiniteQueryOptions,
  cfnResourcesQueryOptions,
  cfnStackQueryOptions,
} from "@/features/cloudformation/data"
import { StackDetail } from "./stack-detail"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children: ReactNode }) => <span>{children}</span>,
}))

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

const STACK_NAME = "orders-api"

function seed(stack: Record<string, unknown>, resources: Record<string, unknown>[] = []) {
  return [
    [cfnStackQueryOptions(STACK_NAME).queryKey, { StackName: STACK_NAME, ...stack }],
    [cfnResourcesQueryOptions(STACK_NAME).queryKey, resources],
  ] as [readonly unknown[], unknown][]
}

/**
 * The failure banner, scoped so assertions cannot be satisfied by the same
 * text appearing in the resources table further down the page.
 */
function banner(): HTMLElement {
  return screen.getByRole("button", { name: "View events" }).parentElement as HTMLElement
}

describe("StackDetail — failure banner", () => {
  // The complaint that started this work: a CDK deploy that failed and rolled
  // back rendered a green "success" badge and no explanation at all, because
  // CloudFormation clears StackStatusReason once a rollback reaches its
  // terminal state.
  it("explains a ROLLBACK_COMPLETE stack even with no stack-level reason", () => {
    renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seed({ StackStatus: "ROLLBACK_COMPLETE" }, [
        { LogicalResourceId: "Bucket", ResourceStatus: "DELETE_COMPLETE" },
        {
          LogicalResourceId: "Queue",
          ResourceStatus: "CREATE_FAILED",
          ResourceStatusReason: "queue name already in use",
        },
      ]),
    )

    // The standing meaning of the status: this stack is delete-only.
    expect(screen.getByText(/can only be deleted/i)).toBeInTheDocument()
    // And the resource that actually failed, which is where the reason lives
    // once the terminal rollback state has cleared StackStatusReason.
    expect(within(banner()).getByText("Queue")).toBeInTheDocument()
    expect(within(banner()).getByText(/queue name already in use/)).toBeInTheDocument()
  })

  it("tells an UPDATE_ROLLBACK_COMPLETE stack it can be updated again", () => {
    renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seed({ StackStatus: "UPDATE_ROLLBACK_COMPLETE" }),
    )

    expect(screen.getByText(/can be updated again/i)).toBeInTheDocument()
    expect(screen.queryByText(/can only be deleted/i)).not.toBeInTheDocument()
    // Recoverable, so the Update action stays offered.
    expect(screen.getByRole("button", { name: "Update" })).toBeInTheDocument()
  })

  it("withholds Update from the delete-only state", () => {
    renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seed({ StackStatus: "ROLLBACK_COMPLETE" }),
    )

    expect(screen.queryByRole("button", { name: "Update" })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument()
  })

  it("prefers the stack-level reason when CloudFormation gives one", () => {
    renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seed(
        {
          StackStatus: "UPDATE_ROLLBACK_FAILED",
          StackStatusReason: "rollback failed: bucket not empty",
        },
        [
          {
            LogicalResourceId: "Bucket",
            ResourceStatus: "DELETE_FAILED",
            ResourceStatusReason: "bucket not empty",
          },
        ],
      ),
    )

    expect(screen.getByText("rollback failed: bucket not empty")).toBeInTheDocument()
  })

  // A rollback that is still running is a deploy that has already failed. The
  // banner should be up before the rollback finishes, not after.
  it("raises the banner while a rollback is still in flight", () => {
    renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seed({ StackStatus: "UPDATE_ROLLBACK_IN_PROGRESS" }),
    )

    expect(screen.getByText(/restoring the previous template/i)).toBeInTheDocument()
  })

  it("shows no banner on a healthy stack", () => {
    renderWithData(<StackDetail stackName={STACK_NAME} />, seed({ StackStatus: "CREATE_COMPLETE" }))

    expect(screen.queryByRole("button", { name: "View events" })).not.toBeInTheDocument()
  })
})

describe("StackDetail — event history", () => {
  it("makes older paginated events explicitly accessible", async () => {
    const events = Array.from({ length: 20 }, (_, i) => ({
      EventId: `event-${i}`,
      LogicalResourceId: `Resource${i}`,
      ResourceType: "AWS::S3::Bucket",
      ResourceStatus: "DELETE_COMPLETE",
    }))
    const seeds = seed({ StackStatus: "ROLLBACK_COMPLETE" })
    seeds.push([
      cfnEventsInfiniteQueryOptions(STACK_NAME).queryKey,
      {
        pages: [{ events, nextToken: "older-events-token" }],
        pageParams: [undefined],
      },
    ])

    const { user } = renderWithData(<StackDetail stackName={STACK_NAME} />, seeds)
    await user.click(screen.getByRole("tab", { name: "Events" }))

    expect(screen.getByRole("region", { name: "CloudFormation stack events" })).toHaveAttribute(
      "tabindex",
      "0",
    )
    expect(screen.getByRole("status")).toHaveTextContent(
      "Showing 20 events. Scroll for older events.",
    )
    expect(screen.queryByRole("button", { name: /older events/i })).not.toBeInTheDocument()
  })
})
