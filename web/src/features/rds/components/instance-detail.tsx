import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  rdsInstanceDetailQueryOptions,
  rdsInstanceLogsQueryOptions,
  rdsKeys,
  startInstanceMutationOptions,
  stopInstanceMutationOptions,
  deleteInstanceMutationOptions,
} from "@/features/rds/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { PageHeader, Spinner, Breadcrumb } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { useState } from "react"
import { Copy, Play, Square, Trash2, RefreshCw } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useToast } from "@/components/ui/toast"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import type { RdsInstance } from "@/types"
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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "RDS Instances", onClick: () => navigate({ to: "/rds" }) },
              { label: db.DBInstanceIdentifier ?? "" },
            ]}
          />
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

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="configuration">Configuration</Tab>
          <Tab id="connectivity">Connectivity</Tab>
          <Tab id="logs">Logs</Tab>
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

function LogsPanel({ instanceId }: { instanceId: string }) {
  const { data, isLoading, refetch, isFetching } = useQuery(rdsInstanceLogsQueryOptions(instanceId))

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
      {data?.logs ? (
        <pre className="max-h-96 overflow-y-auto rounded-md bg-bg-muted p-4 font-mono text-xs leading-relaxed text-fg">
          {data.logs}
        </pre>
      ) : (
        <p className="text-sm text-fg-muted">No logs available</p>
      )}
    </div>
  )
}

// ─── Configuration Panel ──────────────────────────────────────────────────

function ConfigurationPanel({ db }: { db: RdsInstance }) {
  return (
    <div className="grid grid-cols-2 gap-x-8 gap-y-3">
      <InfoRow label="Engine" value={db.Engine ?? ""} />
      <InfoRow label="Version" value={db.EngineVersion ?? ""} />
      <InfoRow label="Instance Class" value={db.DBInstanceClass ?? ""} />
      <InfoRow label="Storage" value={db.AllocatedStorage ? `${db.AllocatedStorage} GB` : "—"} />
      <InfoRow label="Multi-AZ" value={db.MultiAZ ? "Yes" : "No"} />
      <InfoRow label="Master Username" value={db.MasterUsername ?? "—"} />
      <InfoRow
        label="Created"
        value={db.InstanceCreateTime ? db.InstanceCreateTime.toLocaleString() : "—"}
      />
    </div>
  )
}

// ─── Connectivity Panel ───────────────────────────────────────────────────

function ConnectivityPanel({ db }: { db: RdsInstance }) {
  const { toast } = useToast()

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text)
    toast({ title: "Copied!", variant: "success" })
  }

  if (!db.Endpoint) {
    return <p className="text-sm text-fg-muted">Endpoint not yet available.</p>
  }

  const endpointStr = `${db.Endpoint.Address}:${db.Endpoint.Port}`

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <div className="flex items-center gap-2">
          <span className="text-sm font-medium text-fg">Endpoint</span>
          <code className="rounded bg-bg-muted px-2 py-1 font-mono text-sm">{endpointStr}</code>
          <Button
            size="icon"
            variant="ghost"
            className="h-7 w-7"
            onClick={() => copyToClipboard(endpointStr)}
          >
            <Copy className="h-3.5 w-3.5" />
          </Button>
        </div>
        <div className="grid grid-cols-2 gap-x-8 gap-y-3">
          <InfoRow label="Address" value={db.Endpoint.Address ?? ""} />
          <InfoRow label="Port" value={String(db.Endpoint.Port ?? "")} />
          <InfoRow label="VPC" value={db.DBSubnetGroup?.VpcId ?? "—"} />
          <InfoRow label="Subnet Group" value={db.DBSubnetGroup?.DBSubnetGroupName ?? "—"} />
        </div>
      </div>

      <ConnectionStrings db={db} copyToClipboard={copyToClipboard} />
    </div>
  )
}

// ─── Connection Strings ───────────────────────────────────────────────────

function ConnectionStrings({
  db,
  copyToClipboard,
}: {
  db: RdsInstance
  copyToClipboard: (text: string) => void
}) {
  if (!db.Endpoint) return null

  const { Address: address, Port: port } = db.Endpoint
  const user = db.MasterUsername ?? "admin"
  const dbName = db.DBName || db.DBInstanceIdentifier || ""

  const strings = getConnectionStrings(db.Engine ?? "", address ?? "", port ?? 0, user, dbName)
  if (strings.length === 0) return null

  return (
    <div className="space-y-3">
      <span className="text-sm font-medium text-fg">Connection Strings</span>
      <div className="space-y-2">
        {strings.map((s) => (
          <div key={s.label} className="flex items-center gap-2">
            <span className="w-10 shrink-0 text-xs font-medium text-fg-muted">{s.label}</span>
            <code className="min-w-0 flex-1 truncate rounded bg-bg-muted px-2 py-1 font-mono text-xs">
              {s.value}
            </code>
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7 shrink-0"
              onClick={() => copyToClipboard(s.value)}
            >
              <Copy className="h-3.5 w-3.5" />
            </Button>
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

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className="text-sm text-fg">{value}</span>
    </div>
  )
}

function EngineLabel({ engine }: { engine: string }) {
  const label =
    engine === "postgres"
      ? "PostgreSQL"
      : engine === "mysql"
        ? "MySQL"
        : engine === "mariadb"
          ? "MariaDB"
          : engine
  return <span className="text-sm font-medium">{label}</span>
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
