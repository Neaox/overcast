import type { ReactNode } from "react"
import { renderWithData, screen, within } from "@/test/render"
import {
  cfnDiagnosticsQueryOptions,
  cfnEventsInfiniteQueryOptions,
  cfnResourcesQueryOptions,
  cfnStackQueryOptions,
} from "@/features/cloudformation/data"
import type { StackDiagnostics } from "@/services/api/cloudformation"
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

// ─── Diagnostics tab ────────────────────────────────────────────────────────

/**
 * A payload exercising all three section kinds, shaped after the contract in
 * docs/plans/cfn-deploy-diagnostics.md. All three provenance tiers end up on
 * screen: `aws-api` on the service events, `overcast-capture` on both the
 * stopped-task facts and the container output, and `overcast-inference` on the
 * headline, which is the only thing in a payload Overcast itself wrote.
 *
 * The realism matters: the point of the tab is that the useful evidence — the
 * container's own stderr — is the part real AWS would have thrown away, and a
 * fixture of placeholder strings would not catch a renderer that quietly
 * dropped it.
 */
const DIAGNOSTICS: StackDiagnostics = {
  stackName: STACK_NAME,
  stackId: `arn:aws:cloudformation:us-east-1:000000000000:stack/${STACK_NAME}/abc`,
  operation: "CREATE",
  stackStatus: "ROLLBACK_COMPLETE",
  capturedAt: "2026-08-15T04:12:07Z",
  awsReason:
    "(service WebService) is unable to consistently start tasks successfully. " +
    "For more information, see the Troubleshooting section.",
  headline: 'Container "app" exited with code 1 about 6s after starting, 3 times.',
  counterfactual:
    "In real AWS this deploy would have left you only the service event above. " +
    "The container output exists because Overcast captured it before rollback — " +
    "in AWS it would require awslogs on the task definition.",
  resources: [
    {
      logicalId: "WebService",
      physicalId: "arn:aws:ecs:us-east-1:000000000000:service/orders-api-Cluster/WebService",
      type: "AWS::ECS::Service",
      statusReason: "(service WebService) is unable to consistently start tasks successfully.",
      sections: [
        {
          id: "ecs-service-events",
          title: "ECS service events",
          provenance: "aws-api",
          kind: "events",
          events: [
            {
              at: "2026-08-15T04:11:58Z",
              message: "(service WebService) has started 1 tasks: (task 8f3a).",
            },
          ],
        },
        {
          id: "ecs-stopped-tasks",
          title: "Stopped tasks",
          provenance: "overcast-capture",
          note: "Overcast read these off the task before rollback removed it.",
          kind: "facts",
          facts: [{ label: "Exit code", value: "1", hint: "container app" }],
        },
        {
          id: "ecs-container-output",
          title: "Container output",
          // Capture, never inference: this is the container's own stderr, kept
          // verbatim. The inference tier belongs to the headline, which is the
          // only thing in the payload Overcast actually wrote.
          provenance: "overcast-capture",
          kind: "log",
          log: {
            label: "task 8f3a · container app",
            text: "Error: DATABASE_URL is not set\n    at boot (/srv/index.js:12:11)",
            truncated: true,
            capturedAt: "2026-08-15T04:12:03Z",
          },
        },
      ],
    },
  ],
}

function seedDiagnostics(diagnostics: StackDiagnostics | null) {
  const seeds = seed({ StackStatus: "ROLLBACK_COMPLETE" })
  seeds.push([cfnDiagnosticsQueryOptions(STACK_NAME).queryKey, diagnostics])
  return seeds
}

describe("StackDetail — Diagnostics tab", () => {
  // A stack that never failed has no journal, and the endpoint 404s. That is
  // the ordinary case, so it must read as "no tab" rather than as an empty tab
  // or an error — an always-present tab that usually says nothing trains
  // people to ignore the one time it has the answer.
  it("shows no tab when the stack has no diagnostics", () => {
    renderWithData(<StackDetail stackName={STACK_NAME} />, seedDiagnostics(null))

    expect(screen.queryByRole("tab", { name: "Diagnostics" })).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Why did this fail?" })).not.toBeInTheDocument()
  })

  it("shows the tab when a journal exists", () => {
    renderWithData(<StackDetail stackName={STACK_NAME} />, seedDiagnostics(DIAGNOSTICS))

    expect(screen.getByRole("tab", { name: "Diagnostics" })).toBeInTheDocument()
  })

  // The journal alone decides, never the stack's status: the server writes one
  // only for a deploy that did not land and deletes it once one does, so
  // checking the status here would be a second, weaker copy of that rule which
  // hides the answer where the two disagree — a stack redeploying after a
  // failure reads IN_PROGRESS while still holding the last completed deploy's
  // diagnosis.
  //
  // Not asserted here, and deliberately not faked into looking asserted. What
  // would have to be observed is whether the request was made at all, and this
  // harness seeds the query cache directly with staleTime Infinity, so data is
  // returned whatever `enabled` says and a gated component is indistinguishable
  // from an ungated one. The rule that carries the weight — a journal exists if
  // and only if the most recent deploy failed — is pinned server-side, where it
  // is actually decided.
  it("shows the tab on a stack whose status is not a failure", () => {
    const seeds = seed({ StackStatus: "CREATE_IN_PROGRESS" })
    seeds.push([cfnDiagnosticsQueryOptions(STACK_NAME).queryKey, DIAGNOSTICS])

    renderWithData(<StackDetail stackName={STACK_NAME} />, seeds)

    expect(screen.getByRole("tab", { name: "Diagnostics" })).toBeInTheDocument()
  })

  it("renders every section kind", async () => {
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(DIAGNOSTICS),
    )
    await user.click(screen.getByRole("tab", { name: "Diagnostics" }))

    // events
    expect(screen.getByText("ECS service events")).toBeInTheDocument()
    expect(screen.getByText(/has started 1 tasks/)).toBeInTheDocument()
    // facts — label, value and the hint that disambiguates a repeated label
    expect(screen.getByText("Stopped tasks")).toBeInTheDocument()
    expect(screen.getByText("Exit code")).toBeInTheDocument()
    expect(screen.getByText(/^1$/)).toBeInTheDocument()
    expect(screen.getByText("(container app)")).toBeInTheDocument()
    // log — the answer itself, plus the two qualifiers that keep it honest
    expect(screen.getByText("Container output")).toBeInTheDocument()
    expect(screen.getByText(/DATABASE_URL is not set/)).toBeInTheDocument()
    // Both qualifiers in one node: the bare word "Captured" also occurs in the
    // provenance tag, so the assertion has to name the pair to mean anything.
    expect(screen.getByText(/Truncated .* Captured /)).toBeInTheDocument()
  })

  // Wide content must scroll in its own container. A stack trace that widened
  // the document would push the whole console sideways.
  it("gives captured output its own scroll container", async () => {
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(DIAGNOSTICS),
    )
    await user.click(screen.getByRole("tab", { name: "Diagnostics" }))

    const output = screen.getByRole("region", { name: /Captured output/ })
    expect(output).toHaveClass("overflow-auto")
    expect(output).toHaveClass("font-mono")
  })

  // The anti-misleading device of the whole feature: the reader has to be able
  // to tell which parts of this they would also have had in real AWS, and the
  // tier has to say more than "emulator-only" — so both the label and its
  // explanation are asserted, and the explanation is asserted to be readable
  // without a hover.
  it("tags every tier with a label and a reachable explanation", async () => {
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(DIAGNOSTICS),
    )
    await user.click(screen.getByRole("tab", { name: "Diagnostics" }))

    for (const [label, explanation] of [
      ["From the AWS API", /Real AWS returns this too/],
      ["Captured by Overcast", /Real AWS discards it too/],
      ["Overcast's reading", /Not an AWS concept/],
    ] as const) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0)
      const [detail] = screen.getAllByText(explanation)
      expect(detail).toHaveClass("sr-only")
    }
  })

  it("renders the counterfactual at the foot", async () => {
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(DIAGNOSTICS),
    )
    await user.click(screen.getByRole("tab", { name: "Diagnostics" }))

    expect(screen.getByText("In real AWS")).toBeInTheDocument()
    expect(screen.getByText(/would require awslogs on the task definition/)).toBeInTheDocument()
  })

  // A collector can find evidence without being able to draw a conclusion from
  // it. The panel then leads with the sections rather than inventing a summary.
  it("renders a payload with no headline", async () => {
    const { headline: _headline, ...withoutHeadline } = DIAGNOSTICS
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(withoutHeadline),
    )
    await user.click(screen.getByRole("tab", { name: "Diagnostics" }))

    expect(screen.queryByText("What Overcast found")).not.toBeInTheDocument()
    expect(screen.getByText("Container output")).toBeInTheDocument()
    expect(screen.getByText(/DATABASE_URL is not set/)).toBeInTheDocument()
  })

  it("selects the tab from the failure banner", async () => {
    const { user } = renderWithData(
      <StackDetail stackName={STACK_NAME} />,
      seedDiagnostics(DIAGNOSTICS),
    )

    await user.click(within(banner()).getByRole("button", { name: "Why did this fail?" }))

    expect(screen.getByRole("tab", { name: "Diagnostics" })).toHaveAttribute(
      "aria-selected",
      "true",
    )
    expect(screen.getByText(DIAGNOSTICS.headline!)).toBeInTheDocument()
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
