import { renderWithData, screen, within } from "@/test/render"
import {
  ebRulesQueryOptions,
  ebRuleTargetsQueryOptions,
  ebDeliveriesQueryOptions,
} from "@/features/eventbridge/data"
import { EventBusDetail } from "./event-bus-detail"

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

const deleteMutate = vi.fn()
vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: deleteMutate, isPending: false }),
}))

const BUS = "orders-bus"

/** Deliberately not alphabetical, and one rule on another bus. */
const rules = [
  { Name: "ship-order", State: "ENABLED", EventBusName: BUS, Description: "Ships it" },
  { Name: "audit-order", State: "DISABLED", EventBusName: BUS, Description: "" },
  { Name: "elsewhere", State: "ENABLED", EventBusName: "other-bus" },
]

function renderDetail(data: unknown = rules) {
  return renderWithData(<EventBusDetail busName={BUS} />, [
    [ebRulesQueryOptions(BUS).queryKey, data],
    [
      ebRuleTargetsQueryOptions(BUS).queryKey,
      [
        {
          Name: "ship-order",
          Targets: [{ Id: "t1", Arn: "arn:aws:sqs:::ships", TargetType: "SQS" }],
        },
      ],
    ],
    [ebDeliveriesQueryOptions(BUS).queryKey, []],
  ])
}

/** Rule names in render order, header row excluded. */
function nameColumn(): string[] {
  const rows = screen.getAllByRole("row").slice(1)
  return rows.map((row) => within(row).getAllByRole("cell")[0].textContent)
}

describe("EventBusDetail rules table", () => {
  it("shows only this bus's rules, ordered by name", () => {
    renderDetail()
    expect(nameColumn()).toEqual(["audit-order", "ship-order"])
    expect(screen.queryByText("elsewhere")).not.toBeInTheDocument()
  })

  it("reverses the order when the Name header is clicked", async () => {
    const { user } = renderDetail()
    await user.click(screen.getByRole("button", { name: "Name" }))
    expect(nameColumn()).toEqual(["ship-order", "audit-order"])
  })

  it("renders each rule's targets in the embedded table", () => {
    renderDetail()
    expect(screen.getByText("SQS")).toBeInTheDocument()
    expect(screen.getByText("No targets")).toBeInTheDocument()
  })

  it("narrows to the filter, and says so when nothing matches", async () => {
    const { user } = renderDetail()
    const filter = screen.getByRole("textbox", { name: "Filter rules…" })

    await user.type(filter, "ship")
    expect(nameColumn()).toEqual(["ship-order"])

    await user.clear(filter)
    await user.type(filter, "zzz")
    expect(screen.getByText("No rules match the filter.")).toBeInTheDocument()
  })

  it("distinguishes an empty bus from a filter that found nothing", () => {
    renderDetail([])
    expect(screen.getByText("No rules configured on this bus.")).toBeInTheDocument()
  })

  it("confirms before deleting a rule, then deletes by name", async () => {
    const { user } = renderDetail()
    await user.click(screen.getByRole("button", { name: "Delete ship-order" }))
    expect(screen.getByText("Delete Rule")).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Delete" }))
    expect(deleteMutate).toHaveBeenCalledWith("ship-order")
  })
})
