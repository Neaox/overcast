import { renderWithData, screen, within } from "@/test/render"
import {
  rdsInstanceDetailQueryOptions,
  rdsInstanceEventsQueryOptions,
  rdsInstanceLogsQueryOptions,
} from "@/features/rds/data"
import { InstanceDetail } from "./instance-detail"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/components/application-ownership-banner", () => ({
  ApplicationOwnershipBanner: () => null,
}))

const INSTANCE_ID = "orders-db"

const instance = {
  DBInstanceIdentifier: INSTANCE_ID,
  DBInstanceClass: "db.t3.micro",
  Engine: "mysql",
  EngineVersion: "8.0",
  DBInstanceStatus: "failed",
  MasterUsername: "admin",
  AllocatedStorage: 20,
  MultiAZ: false,
  Endpoint: { Address: "orders-db.us-east-1.rds.localhost", Port: 3306 },
  DBInstanceArn: "arn:aws:rds:us-east-1:123456789012:db:orders-db",
}

function seed(logs: unknown, events: unknown = []) {
  return [
    [rdsInstanceDetailQueryOptions(INSTANCE_ID).queryKey, instance],
    [rdsInstanceLogsQueryOptions(INSTANCE_ID).queryKey, logs],
    [rdsInstanceEventsQueryOptions(INSTANCE_ID).queryKey, events],
  ] as [readonly unknown[], unknown][]
}

describe("InstanceDetail — Logs tab", () => {
  // The complaint that started this work: a database that failed to start
  // showed an empty pane, because the container whose logs it wanted was
  // already gone.
  it("explains a failed instance instead of saying there are no logs", async () => {
    const { user } = renderWithData(
      <InstanceDetail instanceId={INSTANCE_ID} />,
      seed({
        instanceId: INSTANCE_ID,
        engine: "mysql",
        status: "failed",
        statusReason: "the database container exited with code 1",
        logs: "[ERROR] --initialize specified but the data directory has files in it. Aborting.\n",
        logSource: "retained",
        capturedAt: "2026-08-04T12:00:00Z",
        message:
          "The database container no longer exists. These are the last logs Overcast captured before it went away.",
      }),
    )

    await user.click(screen.getByRole("tab", { name: "Logs" }))

    expect(screen.getByText("Failure reason")).toBeInTheDocument()
    expect(screen.getByText("the database container exited with code 1")).toBeInTheDocument()
    expect(screen.getByText(/data directory has files in it/)).toBeInTheDocument()
    expect(screen.getByText(/last logs Overcast captured/)).toBeInTheDocument()
    expect(screen.getByText("Retained")).toBeInTheDocument()
    expect(screen.queryByText("No logs available")).not.toBeInTheDocument()
  })

  it("still explains itself when there is nothing to read at all", async () => {
    const { user } = renderWithData(
      <InstanceDetail instanceId={INSTANCE_ID} />,
      seed({
        instanceId: INSTANCE_ID,
        engine: "mysql",
        status: "available",
        logs: "",
        message:
          "No database container was ever started for this DB instance — it is metadata-only.",
      }),
    )

    await user.click(screen.getByRole("tab", { name: "Logs" }))

    expect(screen.getByText(/metadata-only/)).toBeInTheDocument()
    expect(screen.queryByText("No logs available")).not.toBeInTheDocument()
  })

  it("shows live container output without the retained-logs warning", async () => {
    const { user } = renderWithData(
      <InstanceDetail instanceId={INSTANCE_ID} />,
      seed({
        instanceId: INSTANCE_ID,
        engine: "mysql",
        status: "available",
        logs: "ready for connections\n",
        logSource: "container",
      }),
    )

    await user.click(screen.getByRole("tab", { name: "Logs" }))

    expect(screen.getByText(/ready for connections/)).toBeInTheDocument()
    expect(screen.queryByText("Retained")).not.toBeInTheDocument()
  })
})

describe("InstanceDetail — Events tab", () => {
  it("lists events newest-first and marks failures out", async () => {
    const { user } = renderWithData(
      <InstanceDetail instanceId={INSTANCE_ID} />,
      seed({ instanceId: INSTANCE_ID, status: "failed", logs: "" }, [
        {
          SourceIdentifier: INSTANCE_ID,
          SourceType: "db-instance",
          Message: "The DB instance creation failed. the database container exited with code 1.",
          EventCategories: ["failure"],
          Date: new Date("2026-08-04T12:05:00Z"),
        },
        {
          SourceIdentifier: INSTANCE_ID,
          SourceType: "db-instance",
          Message: "DB instance created.",
          EventCategories: ["creation"],
          Date: new Date("2026-08-04T12:00:00Z"),
        },
      ]),
    )

    await user.click(screen.getByRole("tab", { name: "Events" }))

    const rows = screen.getAllByRole("row").slice(1) // drop the header row
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText(/The DB instance creation failed/)).toBeInTheDocument()
    expect(within(rows[0]).getByText("failure")).toBeInTheDocument()
    expect(within(rows[1]).getByText("DB instance created.")).toBeInTheDocument()
    expect(within(rows[1]).getByText("creation")).toBeInTheDocument()
  })

  it("says so when there are no events", async () => {
    const { user } = renderWithData(
      <InstanceDetail instanceId={INSTANCE_ID} />,
      seed({ instanceId: INSTANCE_ID, status: "available", logs: "" }, []),
    )

    await user.click(screen.getByRole("tab", { name: "Events" }))

    expect(screen.getByText("No events in the last 14 days")).toBeInTheDocument()
  })
})
