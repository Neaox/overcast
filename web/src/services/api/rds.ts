import { awsClients } from "../aws-clients"
import { apiFetch } from "./base"
import {
  CreateDBInstanceCommand,
  DescribeDBInstancesCommand,
  DescribeEventsCommand,
  DeleteDBInstanceCommand,
  StartDBInstanceCommand,
  StopDBInstanceCommand,
  type CreateDBInstanceCommandInput,
  type Event,
} from "@aws-sdk/client-rds"

/**
 * The emulator-only logs payload.
 *
 * It is deliberately more than a string of logs: a DB instance that failed to
 * start usually has no container left to read, and the useful answer is the
 * status, the recorded failure reason, and whatever the dying container last
 * said. `logSource` distinguishes a live read from that retained copy so the
 * UI never implies a container that is not there.
 */
export interface RdsInstanceLogs {
  instanceId: string
  engine?: string
  status?: string
  statusReason?: string
  logs: string
  logSource?: "container" | "retained"
  capturedAt?: string
  message?: string
}

/** How far back the Events tab looks — the emulator's full 14-day retention. */
const EVENTS_WINDOW_MINUTES = 14 * 24 * 60

/**
 * Cap on pages followed when collecting an instance's events.
 *
 * `DescribeEvents` returns oldest-first and pages forward, so reading only the
 * first page would show the *oldest* events — the opposite of what a feed
 * wants. The emulator keeps at most 1000 events per region and pages at 100, so
 * ten pages is the whole log; the cap is a guard against a marker loop, not a
 * limit anyone should hit.
 */
const EVENTS_MAX_PAGES = 10

export const rds = {
  listInstances: async () => {
    const res = await awsClients.rds().send(new DescribeDBInstancesCommand({}))
    return res.DBInstances ?? []
  },

  describeInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new DescribeDBInstancesCommand({ DBInstanceIdentifier: identifier }))
    const instances = res.DBInstances ?? []
    if (instances.length === 0) throw new Error(`DB instance "${identifier}" not found`)
    return instances[0]
  },

  createInstance: async (opts: CreateDBInstanceCommandInput) => {
    const res = await awsClients.rds().send(new CreateDBInstanceCommand(opts))
    return res.DBInstance!
  },

  deleteInstance: async (identifier: string) => {
    await awsClients.rds().send(
      new DeleteDBInstanceCommand({
        DBInstanceIdentifier: identifier,
        SkipFinalSnapshot: true,
      }),
    )
  },

  startInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new StartDBInstanceCommand({ DBInstanceIdentifier: identifier }))
    return res.DBInstance!
  },

  stopInstance: async (identifier: string) => {
    const res = await awsClients
      .rds()
      .send(new StopDBInstanceCommand({ DBInstanceIdentifier: identifier }))
    return res.DBInstance!
  },

  // A DB instance with nothing to show answers 404 and still says what it is
  // and why — that body is the diagnosis, so it is data here, not an error.
  getInstanceLogs: (id: string) =>
    apiFetch<RdsInstanceLogs>(`/rds/instances/${encodeURIComponent(id)}/logs`, undefined, {
      acceptStatuses: [404],
    }),

  listInstanceEvents: async (id: string) => {
    const client = awsClients.rds()
    const events: Event[] = []
    let marker: string | undefined

    for (let page = 0; page < EVENTS_MAX_PAGES; page++) {
      const res = await client.send(
        new DescribeEventsCommand({
          SourceIdentifier: id,
          SourceType: "db-instance",
          // Without a Duration the window is the past 60 minutes, which would
          // hide the creation event of any instance older than an hour.
          Duration: EVENTS_WINDOW_MINUTES,
          Marker: marker,
        }),
      )
      events.push(...(res.Events ?? []))
      marker = res.Marker
      if (!marker) break
    }

    // The API returns events oldest-first; a feed reads newest-first.
    return events.reverse()
  },
}
