import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  rdsInstanceDetailQueryOptions,
  rdsInstanceEventsQueryOptions,
  rdsInstanceLogsQueryOptions,
  rdsKeys,
  startInstanceMutationOptions,
  stopInstanceMutationOptions,
  deleteInstanceMutationOptions,
} from "@/features/rds/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { DockerBanner } from "@/components/docker-banner"
import { Badge } from "@/components/ui/badge"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableEmpty,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useState } from "react"
import { Play, Square, Trash2, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import type { RdsInstance } from "@/types"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

export function InstanceDetail({ instanceId }: { instanceId: string }) {
  const navigate = useNavigate()
  const [activeTab, setActiveTab] = useState("configuration")
  const [showDelete, setShowDelete] = useState(false)

  const { data: db, isLoading, isError } = useQuery(rdsInstanceDetailQueryOptions(instanceId))

  const startMut = useResourceMutation({
    options: startInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances(), rdsKeys.instanceDetail(instanceId)],
    successTitle: "DB instance started",
    successDescription: (id) => id,
  })

  const stopMut = useResourceMutation({
    options: stopInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances(), rdsKeys.instanceDetail(instanceId)],
    successTitle: "DB instance stopped",
    successDescription: (id) => id,
    successVariant: "default",
  })

  const deleteMut = useResourceMutation({
    options: deleteInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances()],
    successTitle: "DB instance deleted",
    successDescription: (id) => id,
    successVariant: "default",
    onSuccess: () => navigate({ to: "/rds" }),
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (isError) return null

  if (!db) return null

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={db.DBInstanceIdentifier ?? ""}
        description={
          <span className="flex items-center gap-2">
            <EngineLabel engine={db.Engine ?? ""} />
            <span className="text-fg-muted">{db.EngineVersion}</span>
            <RdsStatusBadge status={db.DBInstanceStatus ?? ""} />
          </span>
        }
        actions={
          <div className="flex gap-1">
            {db.DBInstanceStatus === "stopped" && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => startMut.mutate(db.DBInstanceIdentifier ?? "")}
              >
                <Play className="mr-1.5 h-3.5 w-3.5" />
                Start
              </Button>
            )}
            {db.DBInstanceStatus === "available" && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => stopMut.mutate(db.DBInstanceIdentifier ?? "")}
              >
                <Square className="mr-1.5 h-3.5 w-3.5" />
                Stop
              </Button>
            )}
            <Button
              size="sm"
              variant="ghost"
              className="text-fg-muted hover:text-danger"
              onClick={() => setShowDelete(true)}
            >
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[db.DBInstanceArn, db.DBInstanceIdentifier]} />

      <DockerBanner forService="rds" />

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="configuration">Configuration</Tab>
          <Tab id="connectivity">Connectivity</Tab>
          <Tab id="logs">Logs</Tab>
          <Tab id="events">Events</Tab>
        </TabList>

        <TabPanel id="configuration" className="pt-4">
          <ConfigurationPanel db={db} />
        </TabPanel>

        <TabPanel id="connectivity" className="pt-4">
          <ConnectivityPanel db={db} />
        </TabPanel>

        <TabPanel id="logs" className="pt-4">
          <LogsPanel instanceId={instanceId} />
        </TabPanel>

        <TabPanel id="events" className="pt-4">
          <EventsPanel instanceId={instanceId} />
        </TabPanel>
      </Tabs>

      <ConfirmDialog
        open={showDelete}
        onOpenChange={(v) => !v && setShowDelete(false)}
        title="Delete DB Instance"
        description={
          <>
            Permanently delete <strong>{db.DBInstanceIdentifier}</strong>? This action cannot be
            undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(db.DBInstanceIdentifier ?? "")}
      />
    </div>
  )
}

// ─── Logs Panel ───────────────────────────────────────────────────────────

/**
 * The logs tab exists to answer "why will this database not start?".
 *
 * It used to render `data.logs` and, failing that, the words "No logs
 * available" — in front of a database that had just failed to start, which is
 * exactly when the container is gone and there is nothing live to read. The
 * emulator now sends the instance's status, its recorded failure reason, and
 * the tail it kept from the dying container, and all three are shown here:
 * an empty pane is the one answer this tab must never give.
 */
function LogsPanel({ instanceId }: { instanceId: string }) {
  const { data, isLoading, refetch, isFetching } = useQuery(rdsInstanceLogsQueryOptions(instanceId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  const failed = data?.status === "failed"

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          {data?.status && <RdsStatusBadge status={data.status} />}
          {data?.logSource === "retained" && <Badge variant="warning">Retained</Badge>}
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
      </div>

      {data?.statusReason && (
        <div
          className={cn(
            "rounded-md border p-3 text-[13px]",
            failed ? "border-danger/40 bg-danger/10 text-danger" : "border-border bg-bg-muted",
          )}
        >
          <span className="font-medium">Failure reason</span>
          <p className="mt-1 text-fg">{data.statusReason}</p>
        </div>
      )}

      {data?.message && <p className="text-[13px] text-fg-muted">{data.message}</p>}

      {data?.logs ? (
        <>
          <pre className="max-h-96 overflow-y-auto rounded-md bg-bg-muted p-4 font-mono text-xs leading-relaxed text-fg">
            {data.logs}
          </pre>
          {data.logSource === "retained" && data.capturedAt && (
            <p className="text-xs text-fg-subtle">Captured {formatDate(data.capturedAt)}</p>
          )}
        </>
      ) : (
        !data?.statusReason &&
        !data?.message && <p className="text-sm text-fg-muted">No logs available</p>
      )}
    </div>
  )
}

// ─── Events Panel ─────────────────────────────────────────────────────────

/**
 * The RDS event feed for this instance, newest first — created, started,
 * stopped, deleted, and the failure events that carry the reason a database
 * would not come up. This is AWS's own channel for that question
 * (`DescribeEvents`); the console's failure UI is the same feed rendered.
 *
 * ResourceTable didn't fit because a failure event tints its whole row
 * (`bg-danger/5`) — that is the panel's entire point, and `ResourceTable`
 * has no per-row class/tone hook: it renders `<TableRow key>` and styling is
 * declared per *column* (`cellClassName`), which cannot reach the row. Cell
 * backgrounds do not fill the `<td>` padding, so faking it would leave gaps
 * rather than a tinted row. Reported on #1327 as a `ResourceTable` gap; this
 * panel converts the day a row-tone hook exists.
 */
function EventsPanel({ instanceId }: { instanceId: string }) {
  const { data, isLoading, refetch, isFetching } = useQuery(
    rdsInstanceEventsQueryOptions(instanceId),
  )

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-end">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-52">Time</TableHead>
            <TableHead className="w-40">Categories</TableHead>
            <TableHead>Message</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data ?? []).length === 0 ? (
            <TableEmpty colSpan={3}>No events in the last 14 days</TableEmpty>
          ) : (
            // Keyed by position: the list is replaced wholesale on every
            // refetch, carries no per-row state, and two events can share both
            // timestamp and message.
            (data ?? []).map((event, index) => {
              const categories = event.EventCategories ?? []
              const isFailure = categories.some((c) => c.toLowerCase() === "failure")
              return (
                <TableRow key={index} className={cn(isFailure && "bg-danger/5")}>
                  <TableCell className="text-fg-muted">{formatDate(event.Date)}</TableCell>
                  <TableCell>
                    <span className="flex flex-wrap gap-1">
                      {categories.map((category) => (
                        <Badge
                          key={category}
                          variant={category.toLowerCase() === "failure" ? "danger" : "default"}
                        >
                          {category}
                        </Badge>
                      ))}
                    </span>
                  </TableCell>
                  <TableCellProse className={cn(isFailure && "text-danger")}>
                    {event.Message}
                  </TableCellProse>
                </TableRow>
              )
            })
          )}
        </TableBody>
      </Table>
    </div>
  )
}

// ─── Configuration Panel ──────────────────────────────────────────────────

function ConfigurationPanel({ db }: { db: RdsInstance }) {
  return (
    <DefinitionList>
      <Definition label="Engine" value={db.Engine} />
      <Definition label="Version" value={db.EngineVersion} />
      <Definition label="Instance Class" value={db.DBInstanceClass} />
      <Definition
        label="Storage"
        value={db.AllocatedStorage ? `${db.AllocatedStorage} GB` : null}
      />
      <Definition label="Multi-AZ" value={db.MultiAZ ? "Yes" : "No"} />
      <Definition label="Master Username" value={db.MasterUsername} />
      <Definition
        label="Created"
        value={db.InstanceCreateTime ? db.InstanceCreateTime.toLocaleString() : null}
      />
    </DefinitionList>
  )
}

// ─── Connectivity Panel ───────────────────────────────────────────────────

function ConnectivityPanel({ db }: { db: RdsInstance }) {
  if (!db.Endpoint) {
    return <p className="text-sm text-fg-muted">Endpoint not yet available.</p>
  }

  const endpointStr = `${db.Endpoint.Address}:${db.Endpoint.Port}`

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-medium text-fg">Endpoint</span>
          <code className="rounded bg-bg-muted px-2 py-1 font-mono text-sm">{endpointStr}</code>
          <CopyButton value={endpointStr} noun="endpoint" />
        </div>
        <DefinitionList>
          <Definition label="Address" value={db.Endpoint.Address} />
          <Definition label="Port" value={db.Endpoint.Port} />
          <Definition label="VPC" value={db.DBSubnetGroup?.VpcId} />
          <Definition label="Subnet Group" value={db.DBSubnetGroup?.DBSubnetGroupName} />
        </DefinitionList>
      </div>

      <ConnectionStrings db={db} />
    </div>
  )
}

// ─── Connection Strings ───────────────────────────────────────────────────

function ConnectionStrings({ db }: { db: RdsInstance }) {
  if (!db.Endpoint) return null

  const { Address: address, Port: port } = db.Endpoint
  const user = db.MasterUsername ?? "admin"
  const dbName = db.DBName || db.DBInstanceIdentifier || ""

  const strings = getConnectionStrings(db.Engine ?? "", address ?? "", port ?? 0, user, dbName)
  if (strings.length === 0) return null

  return (
    <div className="space-y-3">
      <span className="font-mono text-sm font-medium text-fg">Connection Strings</span>
      <div className="space-y-2">
        {strings.map((s) => (
          <div key={s.label} className="flex items-center gap-2">
            <span className="w-10 shrink-0 text-xs font-medium text-fg-muted">{s.label}</span>
            <code className="min-w-0 flex-1 truncate rounded bg-bg-muted px-2 py-1 font-mono text-xs">
              {s.value}
            </code>
            <CopyButton value={s.value} noun={`${s.label} connection string`} />
          </div>
        ))}
      </div>
    </div>
  )
}

function getConnectionStrings(
  engine: string,
  address: string,
  port: number,
  user: string,
  dbName: string,
): { label: string; value: string }[] {
  switch (engine) {
    case "postgres":
      return [
        { label: "CLI", value: `psql -h ${address} -p ${port} -U ${user} ${dbName}` },
        { label: "JDBC", value: `jdbc:postgresql://${address}:${port}/${dbName}` },
        { label: "DSN", value: `postgresql://${user}:***@${address}:${port}/${dbName}` },
      ]
    case "mysql":
      return [
        { label: "CLI", value: `mysql -h ${address} -P ${port} -u ${user} -p ${dbName}` },
        { label: "JDBC", value: `jdbc:mysql://${address}:${port}/${dbName}` },
        { label: "DSN", value: `mysql://${user}:***@${address}:${port}/${dbName}` },
      ]
    case "mariadb":
      return [
        { label: "CLI", value: `mariadb -h ${address} -P ${port} -u ${user} -p ${dbName}` },
        { label: "JDBC", value: `jdbc:mariadb://${address}:${port}/${dbName}` },
        { label: "DSN", value: `mariadb://${user}:***@${address}:${port}/${dbName}` },
      ]
    default:
      return []
  }
}

// ─── Shared ───────────────────────────────────────────────────────────────

function EngineLabel({ engine }: { engine: string }) {
  const label =
    engine === "postgres"
      ? "PostgreSQL"
      : engine === "mysql"
        ? "MySQL"
        : engine === "mariadb"
          ? "MariaDB"
          : engine
  return <span className="font-mono text-sm font-medium">{label}</span>
}

function RdsStatusBadge({ status }: { status: string }) {
  const variant =
    status === "available"
      ? "success"
      : status === "creating" ||
          status === "modifying" ||
          status === "starting" ||
          status === "stopping"
        ? "warning"
        : status === "stopped"
          ? "default"
          : status === "deleting" || status === "failed"
            ? "danger"
            : "default"
  return <Badge variant={variant}>{status}</Badge>
}
