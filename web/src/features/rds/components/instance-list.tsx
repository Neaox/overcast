import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Database, Plus, Trash2, RefreshCw, Play, Square, AlertCircle } from "lucide-react"
import { ServiceDocsModal, ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import {
  rdsInstancesQueryOptions,
  rdsKeys,
  createInstanceMutationOptions,
  deleteInstanceMutationOptions,
  startInstanceMutationOptions,
  stopInstanceMutationOptions,
} from "@/features/rds/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Combobox } from "@/components/ui/combobox"
import { FormField, fieldError } from "@/components/ui/form"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { cn } from "@/lib/utils"

export function InstanceList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: instances = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(rdsInstancesQueryOptions())

  const createMut = useResourceMutation({
    options: createInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances()],
    successTitle: "DB instance created",
    successDescription: (opts) => opts.DBInstanceIdentifier,
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances()],
    successTitle: "DB instance deleted",
    successDescription: (id) => id,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const startMut = useResourceMutation({
    options: startInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances()],
    successTitle: "DB instance started",
    successDescription: (id) => id,
  })

  const stopMut = useResourceMutation({
    options: stopInstanceMutationOptions(),
    invalidateKeys: [rdsKeys.instances()],
    successTitle: "DB instance stopped",
    successDescription: (id) => id,
    successVariant: "default",
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="RDS Instances"
        description={`${instances.length} instance${instances.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="rds"
              label="RDS"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create DB Instance
            </Button>
          </>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : instances.length === 0 ? (
        <EmptyState
          icon={<Database className="h-10 w-10" />}
          title="No DB instances"
          description="Create a DB instance to get started."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create DB Instance
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Instance ID</TableHead>
              <TableHead>Engine</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Class</TableHead>
              <TableHead>Endpoint</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {instances.map((db) => (
              <TableRow
                key={db.DBInstanceIdentifier}
                className="cursor-pointer"
                onClick={() =>
                  navigate({
                    to: "/rds/$instance",
                    params: { instance: db.DBInstanceIdentifier ?? "" },
                  })
                }
              >
                <TableCell className="font-medium">{db.DBInstanceIdentifier}</TableCell>
                <TableCell>
                  <EngineLabel engine={db.Engine ?? ""} />
                </TableCell>
                <TableCell className="text-sm text-fg-muted">{db.EngineVersion}</TableCell>
                <TableCell>
                  <RdsStatusBadge status={db.DBInstanceStatus ?? ""} />
                </TableCell>
                <TableCell className="text-sm text-fg-muted">{db.DBInstanceClass}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {db.Endpoint ? `${db.Endpoint.Address}:${db.Endpoint.Port}` : "—"}
                </TableCell>
                <TableCell className="text-xs text-fg-muted">
                  {db.InstanceCreateTime ? db.InstanceCreateTime.toLocaleString() : "—"}
                </TableCell>
                <TableCell>
                  <div className="flex gap-1">
                    {db.DBInstanceStatus === "stopped" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        title="Start"
                        onClick={(e) => {
                          e.stopPropagation()
                          startMut.mutate(db.DBInstanceIdentifier ?? "")
                        }}
                      >
                        <Play className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    {db.DBInstanceStatus === "available" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        title="Stop"
                        onClick={(e) => {
                          e.stopPropagation()
                          stopMut.mutate(db.DBInstanceIdentifier ?? "")
                        }}
                      >
                        <Square className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      title="Delete"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteTarget(db.DBInstanceIdentifier ?? "")
                      }}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateInstanceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete DB Instance"
        description={
          <>
            Permanently delete <strong>{deleteTarget}</strong>? This action cannot be undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Helpers ──────────────────────────────────────────────────────────────

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

// ─── Engine catalog ───────────────────────────────────────────────────────

type EngineReason = "aurora" | "sqlserver" | "oracle" | "db2"

interface EngineEntry {
  value: string
  label: string
  group: string
  supported: true
}

interface UnsupportedEngineEntry {
  value: string
  label: string
  group: string
  supported: false
  reason: EngineReason
}

type AnyEngineEntry = EngineEntry | UnsupportedEngineEntry

const ENGINE_CATALOG: AnyEngineEntry[] = [
  // Supported
  { value: "postgres", label: "PostgreSQL", group: "Supported", supported: true },
  { value: "mysql", label: "MySQL", group: "Supported", supported: true },
  { value: "mariadb", label: "MariaDB", group: "Supported", supported: true },
  // Aurora
  {
    value: "aurora-mysql",
    label: "Aurora MySQL",
    group: "Aurora (Cluster)",
    supported: false,
    reason: "aurora",
  },
  {
    value: "aurora-postgresql",
    label: "Aurora PostgreSQL",
    group: "Aurora (Cluster)",
    supported: false,
    reason: "aurora",
  },
  // SQL Server
  {
    value: "sqlserver-ee",
    label: "SQL Server Enterprise Edition",
    group: "SQL Server",
    supported: false,
    reason: "sqlserver",
  },
  {
    value: "sqlserver-se",
    label: "SQL Server Standard Edition",
    group: "SQL Server",
    supported: false,
    reason: "sqlserver",
  },
  {
    value: "sqlserver-ex",
    label: "SQL Server Express Edition",
    group: "SQL Server",
    supported: false,
    reason: "sqlserver",
  },
  {
    value: "sqlserver-web",
    label: "SQL Server Web Edition",
    group: "SQL Server",
    supported: false,
    reason: "sqlserver",
  },
  // Oracle
  {
    value: "oracle-ee",
    label: "Oracle Enterprise Edition",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "oracle-ee-cdb",
    label: "Oracle Enterprise Edition CDB",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "oracle-se2",
    label: "Oracle Standard Edition 2",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "oracle-se2-cdb",
    label: "Oracle Standard Edition 2 CDB",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "custom-oracle-ee",
    label: "RDS Custom Oracle EE",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "custom-oracle-ee-cdb",
    label: "RDS Custom Oracle EE CDB",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "custom-oracle-se2",
    label: "RDS Custom Oracle SE2",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  {
    value: "custom-oracle-se2-cdb",
    label: "RDS Custom Oracle SE2 CDB",
    group: "Oracle",
    supported: false,
    reason: "oracle",
  },
  // IBM Db2
  {
    value: "db2-ae",
    label: "IBM Db2 Advanced Edition",
    group: "IBM Db2",
    supported: false,
    reason: "db2",
  },
  {
    value: "db2-se",
    label: "IBM Db2 Standard Edition",
    group: "IBM Db2",
    supported: false,
    reason: "db2",
  },
]

const UNSUPPORTED_MESSAGES: Record<EngineReason, string> = {
  aurora:
    "Aurora uses a cluster-based architecture with its own API (CreateDBCluster / CreateDBClusterInstance). Single-instance CreateDBInstance is not applicable. Aurora cluster support is not yet implemented.",
  sqlserver:
    "SQL Server support is not yet implemented in Overcast. A free Docker image is available and this is planned for a future release.",
  oracle:
    "Oracle requires a commercial OTN license and Oracle-provided images that cannot be freely redistributed. Overcast cannot emulate Oracle engines.",
  db2: "IBM Db2 support is not yet implemented in Overcast.",
}

// ─── Create Instance Dialog ───────────────────────────────────────────────

const createSchema = z.object({
  DBInstanceIdentifier: z
    .string()
    .min(1, "Instance ID is required")
    .regex(/^[a-zA-Z][a-zA-Z0-9-]*$/, "Must start with letter, alphanumeric and hyphens only"),
  Engine: z.string().min(1, "Engine is required"),
  EngineVersion: z.string(),
  MasterUsername: z.string().min(1, "Username is required"),
  MasterUserPassword: z.string().min(8, "Password must be at least 8 characters"),
  DBInstanceClass: z.string().min(1, "Instance class is required"),
  AllocatedStorage: z.number().int().min(5, "Min 5 GB").max(1000, "Max 1000 GB"),
})

function CreateInstanceDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (opts: {
    DBInstanceIdentifier: string
    Engine: string
    EngineVersion?: string
    MasterUsername: string
    MasterUserPassword: string
    DBInstanceClass: string
    AllocatedStorage: number
  }) => void
}) {
  const [showEngineDocs, setShowEngineDocs] = useState(false)
  const form = useForm({
    validators: { onChange: createSchema },
    defaultValues: {
      DBInstanceIdentifier: "",
      Engine: "postgres",
      EngineVersion: "",
      MasterUsername: "admin",
      MasterUserPassword: "",
      DBInstanceClass: "db.t3.micro",
      AllocatedStorage: 20,
    },
    onSubmit: ({ value }) =>
      onSubmit({
        ...value,
        EngineVersion: value.EngineVersion || undefined,
      }),
  })

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(v) => {
          if (!v) {
            onClose()
            form.reset()
          }
        }}
      >
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Create DB Instance</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <DialogBody className="space-y-4">
              <form.Field name="DBInstanceIdentifier">
                {(field) => (
                  <FormField
                    label="Instance ID"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Input
                      placeholder="my-database"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      onBlur={field.handleBlur}
                    />
                  </FormField>
                )}
              </form.Field>
              <form.Field name="Engine">
                {(field) => {
                  const selectedEntry = ENGINE_CATALOG.find((e) => e.value === field.state.value)
                  const isUnsupported = selectedEntry && !selectedEntry.supported
                  return (
                    <FormField
                      label="Engine"
                      error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                    >
                      <Combobox<AnyEngineEntry>
                        value={field.state.value}
                        onChange={(v) => field.handleChange(v)}
                        items={ENGINE_CATALOG}
                        filterFn={(item, q) =>
                          item.label.toLowerCase().includes(q.toLowerCase()) ||
                          item.value.toLowerCase().includes(q.toLowerCase())
                        }
                        getItemValue={(item) => item.value}
                        isItemDisabled={(item) =>
                          item.supported ? undefined : "Not supported in Overcast"
                        }
                        renderSeparator={(item, prev) =>
                          prev === null || item.group !== prev.group ? (
                            <li className="px-3 pt-2 pb-0.5 text-xs font-semibold tracking-wide text-fg-muted uppercase">
                              {item.group}
                            </li>
                          ) : null
                        }
                        renderItem={(item, { selected, disabled }) => (
                          <span className="flex items-center justify-between gap-2">
                            <span className={cn(disabled && "text-fg-muted")}>{item.label}</span>
                            {selected && !disabled && (
                              <span className="text-xs text-accent">✓</span>
                            )}
                            {disabled && (
                              <span className="shrink-0 text-xs text-fg-muted">unavailable</span>
                            )}
                          </span>
                        )}
                        placeholder="Search engines…"
                      />
                      {isUnsupported && (
                        <div className="mt-2 flex items-start gap-2 rounded border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-fg">
                          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warning" />
                          <span>
                            {UNSUPPORTED_MESSAGES[selectedEntry.reason]}{" "}
                            <button
                              type="button"
                              className="underline underline-offset-2 hover:text-accent"
                              onClick={() => setShowEngineDocs(true)}
                            >
                              Learn more in RDS docs.
                            </button>
                          </span>
                        </div>
                      )}
                    </FormField>
                  )
                }}
              </form.Field>
              <form.Field name="EngineVersion">
                {(field) => (
                  <FormField
                    label="Engine Version (optional)"
                    error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                  >
                    <Input
                      placeholder="16.1"
                      value={field.state.value}
                      onChange={(e) => field.handleChange(e.target.value)}
                      onBlur={field.handleBlur}
                    />
                  </FormField>
                )}
              </form.Field>
              <div className="grid grid-cols-2 gap-4">
                <form.Field name="MasterUsername">
                  {(field) => (
                    <FormField
                      label="Master Username"
                      error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                    >
                      <Input
                        placeholder="admin"
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                        onBlur={field.handleBlur}
                      />
                    </FormField>
                  )}
                </form.Field>
                <form.Field name="MasterUserPassword">
                  {(field) => (
                    <FormField
                      label="Master Password"
                      error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                    >
                      <Input
                        type="password"
                        placeholder="••••••••"
                        value={field.state.value}
                        onChange={(e) => field.handleChange(e.target.value)}
                        onBlur={field.handleBlur}
                      />
                    </FormField>
                  )}
                </form.Field>
              </div>
              <div className="grid grid-cols-2 gap-4">
                <form.Field name="DBInstanceClass">
                  {(field) => (
                    <FormField
                      label="Instance Class"
                      error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                    >
                      <Combobox<{ value: string }>
                        value={field.state.value}
                        onChange={(v) => field.handleChange(v)}
                        items={[
                          { value: "db.t3.micro" },
                          { value: "db.t3.small" },
                          { value: "db.t3.medium" },
                          { value: "db.m5.large" },
                        ]}
                        filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                        getItemValue={(item) => item.value}
                        renderItem={(item) => item.value}
                        allowCustom
                        placeholder="Select instance class…"
                      />
                    </FormField>
                  )}
                </form.Field>
                <form.Field name="AllocatedStorage">
                  {(field) => (
                    <FormField
                      label="Storage (GB)"
                      error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                    >
                      <Input
                        type="number"
                        min={5}
                        max={1000}
                        value={field.state.value}
                        onChange={(e) => field.handleChange(parseInt(e.target.value) || 20)}
                        onBlur={field.handleBlur}
                      />
                    </FormField>
                  )}
                </form.Field>
              </div>
            </DialogBody>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={onClose}>
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={
                  isPending ||
                  !!ENGINE_CATALOG.find(
                    (e) => e.value === form.getFieldValue("Engine") && !e.supported,
                  )
                }
              >
                {isPending && <Spinner className="mr-2" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ServiceDocsModal
        service="rds"
        label="RDS"
        open={showEngineDocs}
        onClose={() => setShowEngineDocs(false)}
      />
    </>
  )
}
