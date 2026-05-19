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
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
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
          <span className="text-muted-foreground font-mono text-xs">{api.uris.GRAPHQL}</span>
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
              tab === key
                ? "border-primary text-foreground border-b-2"
                : "text-muted-foreground hover:text-foreground",
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
  const { data: dataSources = [], isLoading } = useQuery(appsyncDataSourcesQueryOptions(apiId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (dataSources.length === 0) {
    return (
      <EmptyState
        icon={<Database className="h-6 w-6" />}
        title="No data sources"
        description="Add data sources via the AWS SDK or CLI."
      />
    )
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Type</TableHead>
          <TableHead>Description</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {dataSources.map((ds) => (
          <TableRow key={ds.name}>
            <TableCell className="font-mono text-sm">{ds.name}</TableCell>
            <TableCell>
              <Badge variant="outline">{ds.type}</Badge>
            </TableCell>
            <TableCell className="text-muted-foreground text-sm">{ds.description || "—"}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
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
  const { data: resolvers = [], isLoading } = useQuery(
    appsyncResolversQueryOptions(apiId, typeName),
  )

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold">{typeName}</h3>
      {isLoading ? (
        <div className="flex justify-center py-4">
          <Spinner className="h-4 w-4" />
        </div>
      ) : resolvers.length === 0 ? (
        <p className="text-muted-foreground text-sm">No resolvers for {typeName}.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Field</TableHead>
              <TableHead>Data Source</TableHead>
              <TableHead>Kind</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {resolvers.map((r) => (
              <TableRow key={`${r.typeName}.${r.fieldName}`}>
                <TableCell className="font-mono text-sm">{r.fieldName}</TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {r.dataSourceName || "—"}
                </TableCell>
                <TableCell>
                  <Badge variant="outline">{r.kind}</Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

// ─── Functions Tab ─────────────────────────────────────────────────────────

function FunctionsTab({ apiId }: { apiId: string }) {
  const { data: functions = [], isLoading } = useQuery(appsyncFunctionsQueryOptions(apiId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (functions.length === 0) {
    return (
      <EmptyState
        icon={<FunctionSquare className="h-6 w-6" />}
        title="No functions"
        description="Add pipeline functions via the AWS SDK or CLI."
      />
    )
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Data Source</TableHead>
          <TableHead>Description</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {functions.map((fn) => (
          <TableRow key={fn.functionId}>
            <TableCell className="font-mono text-sm">{fn.name}</TableCell>
            <TableCell className="text-muted-foreground text-sm">
              {fn.dataSourceName || "—"}
            </TableCell>
            <TableCell className="text-muted-foreground text-sm">{fn.description || "—"}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
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
  const { data: apiKeys = [], isLoading } = useQuery(appsyncApiKeysQueryOptions(apiId))

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

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

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

      {apiKeys.length === 0 ? (
        <EmptyState
          icon={<Key className="h-6 w-6" />}
          title="No API keys"
          description="Create an API key for API_KEY authentication."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Key ID</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {apiKeys.map((k) => (
              <TableRow key={k.id}>
                <TableCell className="font-mono text-xs">{k.id}</TableCell>
                <TableCell className="text-muted-foreground text-sm">
                  {k.description || "—"}
                </TableCell>
                <TableCell className="text-muted-foreground text-xs">
                  {k.expires ? new Date(k.expires * 1000).toLocaleDateString() : "—"}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteKeyTarget(k.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <ConfirmDialog
        open={!!deleteKeyTarget}
        onOpenChange={(open) => !open && setDeleteKeyTarget(undefined)}
        title="Delete API Key"
        description={
          <>
            Delete API key <span className="font-mono font-semibold">{deleteKeyTarget}</span>?
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteKeyTarget && deleteMut.mutate(deleteKeyTarget)}
      />
    </div>
  )
}

// ─── Schema Tab ────────────────────────────────────────────────────────────

function SchemaTab({ apiId }: { apiId: string }) {
  const { data: schemaStatus, isLoading: statusLoading } = useQuery(
    appsyncSchemaStatusQueryOptions(apiId),
  )
  const { data: types = [], isLoading: typesLoading } = useQuery(appsyncTypesQueryOptions(apiId))

  if (statusLoading || typesLoading) {
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
          <span className="text-sm font-medium">Schema Status:</span>
          <Badge variant={schemaStatus.status === "ACTIVE" ? "default" : "outline"}>
            {schemaStatus.status}
          </Badge>
          {schemaStatus.details && (
            <span className="text-muted-foreground text-xs">{schemaStatus.details}</span>
          )}
        </div>
      )}

      {types.length === 0 ? (
        <EmptyState
          icon={<FileCode className="h-6 w-6" />}
          title="No schema"
          description="Upload a schema via the AWS SDK or CLI."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Type Name</TableHead>
              <TableHead>Format</TableHead>
              <TableHead>Definition</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {types.map((t) => (
              <TableRow key={t.name}>
                <TableCell className="font-mono text-sm font-semibold">{t.name}</TableCell>
                <TableCell>
                  <Badge variant="outline">{t.format}</Badge>
                </TableCell>
                <TableCell>
                  <pre className="text-muted-foreground max-w-lg truncate font-mono text-xs">
                    {t.definition}
                  </pre>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
