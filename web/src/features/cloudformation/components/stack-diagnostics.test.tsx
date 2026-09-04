import { render, screen } from "@/test/render"
import type { StackDiagnostics } from "@/services/api/cloudformation"
import { StackDiagnosticsPanel } from "./stack-diagnostics"

/**
 * The panel renders whatever the capture endpoint returns, and the server omits
 * a resource's `sections` entirely (`json:"sections,omitempty"`) whenever no
 * collector covers its type. `diagnosticCollectors` has one entry —
 * AWS::ECS::Service — so that is every other resource type, which made the
 * whole tab throw for most failures:
 *
 *   TypeError: can't access property "map", e.sections is undefined
 *
 * A resource with no collector is a legitimate entry, not a malformed one: it
 * still carries the reason CloudFormation recorded, and correctly offers no
 * evidence beyond it.
 */
function diagnostics(overrides: Partial<StackDiagnostics> = {}): StackDiagnostics {
  return {
    stackName: "ManagedServicesTaskServiceStack",
    operation: "CREATE",
    stackStatus: "ROLLBACK_COMPLETE",
    capturedAt: "2026-09-04T04:41:26.226Z",
    counterfactual: "",
    resources: [],
    ...overrides,
  }
}

describe("StackDiagnosticsPanel", () => {
  it("renders a resource whose type has no collector, so carries no sections", () => {
    render(
      <StackDiagnosticsPanel
        diagnostics={diagnostics({
          resources: [
            {
              logicalId: "MSTasksDatabaseInstance1E05BD5BD",
              type: "AWS::RDS::DBInstance",
              statusReason:
                "VPC 'vpc-b7732331' is not launchable for DB instances (network status=unbacked).",
            },
          ],
        })}
      />,
    )

    // The entry is present and carries the one sentence it does have.
    expect(screen.getByText("MSTasksDatabaseInstance1E05BD5BD")).toBeInTheDocument()
    expect(screen.getByText(/network status=unbacked/)).toBeInTheDocument()
  })

  it("still renders the sections a resource does carry", () => {
    render(
      <StackDiagnosticsPanel
        diagnostics={diagnostics({
          resources: [
            {
              logicalId: "ManagedServicesTaskServiceFargateTaskService",
              type: "AWS::ECS::Service",
              sections: [
                {
                  id: "stopped-tasks",
                  title: "Stopped tasks",
                  provenance: "overcast-capture",
                  kind: "facts",
                  facts: [{ label: "Stopped reason", value: "ResourceInitializationError" }],
                },
              ],
            },
          ],
        })}
      />,
    )

    expect(screen.getByText("Stopped tasks")).toBeInTheDocument()
    expect(screen.getByText("ResourceInitializationError")).toBeInTheDocument()
  })
})
