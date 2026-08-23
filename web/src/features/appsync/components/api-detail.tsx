import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  type Braces,
  Trash2,
  RefreshCw,
  Database,
  Key,
  GitBranch,
  FunctionSquare,
  FileCode,
} from "lucide-react"
import {
  appsyncApiQueryOptions,
  appsyncDataSourcesQueryOptions,
  appsyncFunctionsQueryOptions,
  appsyncApiKeysQueryOptions,
  appsyncSchemaStatusQueryOptions,
  appsyncTypesQueryOptions,
  appsyncResolversQueryOptions,
  appsyncKeys,
  deleteApiMutationOptions,
  createApiKeyMutationOptions,
  deleteApiKeyMutationOptions,
} from "@/features/appsync/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ResourceTable } from "@/components/ui/resource-table"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { cn } from "@/lib/utils"

interface Props {
  apiId: string
}

type Tab = "dataSources" | "resolvers" | "functions" | "apiKeys" | "schema"

export function ApiDetail({ apiId }: Props) {
  const navigate = useNavigate()
  const [tab, setTab] = useState<Tab>("dataSources")
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleteKeyTarget, setDeleteKeyTarget] = useState<string>()

  const { data: api, isLoading, refetch, isFetching } = useQuery(appsyncApiQueryOptions(apiId))

  const deleteMut = useResourceMutation({
    options: deleteApiMutationOptions(),
    invalidateKeys: [appsyncKeys.apis()],
    successTitle: "GraphQL API deleted",
    onSuccess: () => void navigate({ to: "/appsync" }),
  })

  if (isLoading || !api) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={api.name ?? ""}
        description={api.apiId ?? ""}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="text-danger hover:text-danger"
              onClick={() => setDeleteOpen(true)}
            >
              <Trash2 className="mr-1.5 h-3.5 w-3.5" />
              Delete
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[api.arn, api.apiId, api.name]} />

      {/* API metadata */}
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="default">{api.authenticationType}</Badge>
        {api.uris?.GRAPHQL && (
          <span className="font-mono text-xs text-fg-subtle">{api.uris.GRAPHQL}</span>
        )}
      </div>

      {/* Tabs */}
      <div className="flex gap-0 border-b border-border">
        {TABS.map(({ key, label, icon: Icon }) => (
          <button
            key={key}
            onClick={() => setTab(key)}
            className={cn(
              "flex items-center gap-1.5 px-3 py-2 text-sm font-medium transition-colors",
              tab === key ? "border-b-2 border-accent text-fg" : "text-fg-muted hover:text-fg",
            )}
          >
            <Icon className="h-3.5 w-3.5" />
            {label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === "dataSources" && <DataSourcesTab apiId={apiId} />}
      {tab === "resolvers" && <ResolversTab apiId={apiId} />}
      {tab === "functions" && <FunctionsTab apiId={apiId} />}
      {tab === "apiKeys" && (
        <ApiKeysTab
          apiId={apiId}
          deleteKeyTarget={deleteKeyTarget}
          setDeleteKeyTarget={setDeleteKeyTarget}
        />
      )}
      {tab === "schema" && <SchemaTab apiId={apiId} />}

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="Delete GraphQL API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{api.name}</span> and all its
            sub-resources? This cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteMut.mutate(apiId)}
      />
    </div>
  )
}

const TABS: { key: Tab; label: string; icon: typeof Braces }[] = [
  { key: "dataSources", label: "Data Sources", icon: Database },
  { key: "resolvers", label: "Resolvers", icon: GitBranch },
  { key: "functions", label: "Functions", icon: FunctionSquare },
  { key: "apiKeys", label: "API Keys", icon: Key },
  { key: "schema", label: "Schema", icon: FileCode },
]

// ─── Data Sources Tab ──────────────────────────────────────────────────────

function DataSourcesTab({ apiId }: { apiId: string }) {
  const {
    data: dataSources = [],
    isLoading,
    error,
  } = useQuery(appsyncDataSourcesQueryOptions(apiId))

  return (
    <ResourceTable
      variant="embedded"
      columnToggle={false}
      query={{ data: dataSources, isLoading, error }}
      noun="data sources"
      emptyIcon={Database}
      emptyTitle="No data sources"
      emptyDescription="Add data sources via the AWS SDK or CLI."
      errorTitle="Failed to load data sources"
      rowKey={(ds) => ds.name ?? ""}
      columns={[
        { header: "Name", sortValue: (ds) => ds.name, cell: (ds) => ds.name },
        { header: "Type", cell: (ds) => <Badge variant="outline">{ds.type}</Badge> },
        { header: "Description", prose: true, cell: (ds) => ds.description || "—" },
      ]}
    />
  )
}

// ─── Resolvers Tab ─────────────────────────────────────────────────────────

function ResolversTab({ apiId }: { apiId: string }) {
  const { data: types = [], isLoading: typesLoading } = useQuery(appsyncTypesQueryOptions(apiId))

  // Collect root types to list resolvers for
  const rootTypes = types
    .map((t) => t.name)
    .filter(
      (name): name is string => !!name && ["Query", "Mutation", "Subscription"].includes(name),
    )

  if (typesLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (rootTypes.length === 0) {
    return (
      <EmptyState
        icon={<GitBranch className="h-6 w-6" />}
        title="No resolvers"
        description="Create a schema with Query/Mutation types, then add resolvers."
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {rootTypes.map((typeName) => (
        <ResolverGroup key={typeName} apiId={apiId} typeName={typeName} />
      ))}
    </div>
  )
}

function ResolverGroup({ apiId, typeName }: { apiId: string; typeName: string }) {
  const {
    data: resolvers = [],
    isLoading,
    error,
  } = useQuery(appsyncResolversQueryOptions(apiId, typeName))

  return (
    <div>
      <h3 className="mb-2 font-mono text-sm font-semibold">{typeName}</h3>
      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: resolvers, isLoading, error }}
        noun="resolvers"
        emptyTitle={`No resolvers for ${typeName}.`}
        errorTitle={`Failed to load resolvers for ${typeName}`}
        loadingCount={2}
        rowKey={(r) => `${r.typeName}.${r.fieldName}`}
        columns={[
          { header: "Field", sortValue: (r) => r.fieldName, cell: (r) => r.fieldName },
          {
            header: "Data Source",
            cellClassName: "text-fg-muted",
            cell: (r) => r.dataSourceName || "—",
          },
          { header: "Kind", cell: (r) => <Badge variant="outline">{r.kind}</Badge> },
        ]}
      />
    </div>
  )
}

// ─── Functions Tab ─────────────────────────────────────────────────────────

function FunctionsTab({ apiId }: { apiId: string }) {
  const { data: functions = [], isLoading, error } = useQuery(appsyncFunctionsQueryOptions(apiId))

  return (
    <ResourceTable
      variant="embedded"
      columnToggle={false}
      query={{ data: functions, isLoading, error }}
      noun="functions"
      emptyIcon={FunctionSquare}
      emptyTitle="No functions"
      emptyDescription="Add pipeline functions via the AWS SDK or CLI."
      errorTitle="Failed to load functions"
      rowKey={(fn) => fn.functionId ?? ""}
      columns={[
        { header: "Name", sortValue: (fn) => fn.name, cell: (fn) => fn.name },
        {
          header: "Data Source",
          cellClassName: "text-fg-muted",
          cell: (fn) => fn.dataSourceName || "—",
        },
        { header: "Description", prose: true, cell: (fn) => fn.description || "—" },
      ]}
    />
  )
}

// ─── API Keys Tab ──────────────────────────────────────────────────────────

function ApiKeysTab({
  apiId,
  deleteKeyTarget,
  setDeleteKeyTarget,
}: {
  apiId: string
  deleteKeyTarget?: string
  setDeleteKeyTarget: (v: string | undefined) => void
}) {
  const { data: apiKeys = [], isLoading, error } = useQuery(appsyncApiKeysQueryOptions(apiId))

  const createMut = useResourceMutation({
    options: createApiKeyMutationOptions(apiId),
    invalidateKeys: [appsyncKeys.apiKeys(apiId)],
    successTitle: "API key created",
  })

  const deleteMut = useResourceMutation({
    options: deleteApiKeyMutationOptions(apiId),
    invalidateKeys: [appsyncKeys.apiKeys(apiId)],
    successTitle: "API key deleted",
    onSuccess: () => setDeleteKeyTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <Button
          size="sm"
          onClick={() => createMut.mutate(undefined)}
          disabled={createMut.isPending}
        >
          <Key className="mr-1.5 h-3.5 w-3.5" />
          Create Key
        </Button>
      </div>

      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: apiKeys, isLoading, error }}
        noun="API keys"
        emptyIcon={Key}
        emptyTitle="No API keys"
        emptyDescription="Create an API key for API_KEY authentication."
        errorTitle="Failed to load API keys"
        rowKey={(k) => k.id ?? ""}
        columns={[
          { header: "Key ID", sortValue: (k) => k.id, cell: (k) => k.id },
          { header: "Description", prose: true, cell: (k) => k.description || "—" },
          {
            header: "Expires",
            cellClassName: "text-fg-muted",
            sortValue: (k) => k.expires ?? 0,
            cell: (k) => (k.expires ? new Date(k.expires * 1000).toLocaleDateString() : "—"),
          },
        ]}
        onDelete={{
          // The target lives on the parent page (it survives a tab switch), so
          // the row is resolved from the id rather than held here.
          target: apiKeys.find((k) => k.id === deleteKeyTarget),
          onRequest: (k) => setDeleteKeyTarget(k.id),
          onOpenChange: (open) => !open && setDeleteKeyTarget(undefined),
          mutation: deleteMut,
          getId: (k) => k.id ?? "",
          label: (k) => k.id ?? "",
          noun: "API key",
          title: "Delete API Key",
          description: (k) => (
            <>
              Delete API key <span className="font-mono font-semibold">{k.id}</span>?
            </>
          ),
        }}
      />
    </div>
  )
}

// ─── Schema Tab ────────────────────────────────────────────────────────────

function SchemaTab({ apiId }: { apiId: string }) {
  const { data: schemaStatus, isLoading: statusLoading } = useQuery(
    appsyncSchemaStatusQueryOptions(apiId),
  )
  const {
    data: types = [],
    isLoading: typesLoading,
    error: typesError,
  } = useQuery(appsyncTypesQueryOptions(apiId))

  if (statusLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {schemaStatus && (
        <div className="flex items-center gap-2">
          <span className="font-mono text-sm font-medium">Schema Status:</span>
          <Badge variant={schemaStatus.status === "ACTIVE" ? "default" : "outline"}>
            {schemaStatus.status}
          </Badge>
          {schemaStatus.details && (
            <span className="text-xs text-fg-subtle">{schemaStatus.details}</span>
          )}
        </div>
      )}

      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: types, isLoading: typesLoading, error: typesError }}
        noun="types"
        emptyIcon={FileCode}
        emptyTitle="No schema"
        emptyDescription="Upload a schema via the AWS SDK or CLI."
        errorTitle="Failed to load schema types"
        rowKey={(t) => t.name ?? ""}
        columns={[
          {
            header: "Type Name",
            cellClassName: "font-semibold",
            sortValue: (t) => t.name,
            cell: (t) => t.name,
          },
          { header: "Format", cell: (t) => <Badge variant="outline">{t.format}</Badge> },
          {
            header: "Definition",
            cell: (t) => (
              <pre className="max-w-lg truncate font-mono text-xs text-fg-subtle">
                {t.definition}
              </pre>
            ),
          },
        ]}
      />
    </div>
  )
}
