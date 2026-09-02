import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Database, Eye, Play, Square, AlertCircle } from "lucide-react"
import {
  ServiceDocsModal,
  ServiceDocsButton,
  useDocsFromHash,
} from "@/features/docs/service-docs-modal"
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
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { DockerBanner } from "@/components/docker-banner"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { RdsInstance } from "@/types"

interface InstanceListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function InstanceList({ sort, onSortChange }: InstanceListProps = {}) {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<RdsInstance>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: instances = [],
    isLoading,
    isFetching,
    refetch,
    error,
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
    <ResourceListPage
      title="RDS Instances"
      count={instances.length}
      actions={
        <>
          <ServiceDocsButton
            service="rds"
            label="RDS"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create DB instance</CreateAction>
        </>
      }
    >
      <DockerBanner forService="rds" />

      <ResourceTable
        query={{ data: instances, isLoading, error }}
        noun="DB instances"
        emptyIcon={Database}
        emptyTitle="No DB instances"
        emptyDescription="Create a DB instance to get started."
        emptyAction={
          <CreateAction onClick={() => setShowCreate(true)}>Create DB instance</CreateAction>
        }
        errorTitle="Failed to load DB instances"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(db) => db.DBInstanceIdentifier ?? ""}
        onRowClick={(db) =>
          navigate({ to: "/rds/$instance", params: { instance: db.DBInstanceIdentifier ?? "" } })
        }
        columns={[
          {
            id: "instance",
            header: "Instance ID",
            sortValue: (db) => db.DBInstanceIdentifier,
            cell: (db) => <ResourceName icon={Database} name={db.DBInstanceIdentifier ?? ""} />,
          },
          { header: "Engine", cell: (db) => <EngineLabel engine={db.Engine ?? ""} /> },
          { header: "Version", cellClassName: "text-fg-muted", cell: (db) => db.EngineVersion },
          {
            header: "Status",
            cell: (db) => <RdsStatusBadge status={db.DBInstanceStatus ?? ""} />,
          },
          { header: "Class", cellClassName: "text-fg-muted", cell: (db) => db.DBInstanceClass },
          {
            header: "Endpoint",
            cellClassName: "text-fg-muted",
            cell: (db) => (db.Endpoint ? `${db.Endpoint.Address}:${db.Endpoint.Port}` : "—"),
          },
          {
            id: "created",
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (db) => db.InstanceCreateTime,
            cell: (db) => (db.InstanceCreateTime ? db.InstanceCreateTime.toLocaleString() : "—"),
          },
        ]}
        rowActions={(db) => {
          const instance = db.DBInstanceIdentifier ?? ""
          return (
            <>
              {db.DBInstanceStatus === "stopped" && (
                <RowAction label={`Start ${instance}`} onClick={() => startMut.mutate(instance)}>
                  <Play className="h-3.5 w-3.5" />
                </RowAction>
              )}
              {db.DBInstanceStatus === "available" && (
                <RowAction label={`Stop ${instance}`} onClick={() => stopMut.mutate(instance)}>
                  <Square className="h-3.5 w-3.5" />
                </RowAction>
              )}
              <RowAction
                label={`View ${instance}`}
                onClick={() => navigate({ to: "/rds/$instance", params: { instance } })}
              >
                <Eye className="h-3.5 w-3.5" />
              </RowAction>
            </>
          )
        }}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (v) => !v && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (db) => db.DBInstanceIdentifier ?? "",
          label: (db) => db.DBInstanceIdentifier ?? "",
          noun: "DB instance",
          title: "Delete DB Instance",
          description: (db) => (
            <>
              Permanently delete <strong>{db.DBInstanceIdentifier}</strong>? This action cannot be
              undone.
            </>
          ),
        }}
      />

      <CreateInstanceDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />
    </ResourceListPage>
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
                            <li className={cn(sectionLabel, "px-3 pt-2 pb-0.5 text-fg-muted")}>
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
                        <div className="mt-2 flex items-start gap-2 rounded border border-warning/40 bg-warning-muted px-3 py-2 text-xs text-fg">
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
