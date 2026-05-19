import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Workflow, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  appsyncApisQueryOptions,
  appsyncKeys,
  deleteApiMutationOptions,
  createApiMutationOptions,
} from "@/features/appsync/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreateResourceDialog } from "@/components/create-resource-dialog"
import { cn } from "@/lib/utils"

export function AppSyncPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ name: string; apiId: string }>()
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()
  const navigate = useNavigate()

  const { data: apis = [], isLoading, isFetching, refetch } = useQuery(appsyncApisQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteApiMutationOptions(),
    invalidateKeys: [appsyncKeys.apis()],
    successTitle: "GraphQL API deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? apis.filter((a) => (a.name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : apis,
    [apis, filter],
  )

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="AppSync"
        description={`${apis.length} GraphQL API${apis.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="appsync"
              label="AppSync"
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
              Create API
            </Button>
          </>
        }
      />
      <div className="relative">
        <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
        <Input
          placeholder="Filter APIs…"
          className="pl-8"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Workflow className="h-6 w-6" />}
          title="No GraphQL APIs"
          description={
            filter ? "No APIs match the filter." : "Create a GraphQL API to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create API
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>API ID</TableHead>
              <TableHead>Auth Type</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((api) => (
              <TableRow
                key={api.apiId}
                className="cursor-pointer"
                onClick={() =>
                  navigate({ to: "/appsync/$apiId", params: { apiId: api.apiId ?? "" } })
                }
              >
                <TableCell className="font-mono text-sm">{api.name}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">
                  {api.apiId}
                </TableCell>
                <TableCell>
                  <Badge variant="default">{api.authenticationType}</Badge>
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget({ name: api.name ?? "", apiId: api.apiId ?? "" })
                    }}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create GraphQL API"
        label="API Name"
        placeholder="my-graphql-api"
        mutationOptions={createApiMutationOptions}
        invalidateKeys={[appsyncKeys.apis()]}
        successTitle="GraphQL API created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete GraphQL API"
        description={
          <>
            Delete <span className="font-mono font-semibold">{deleteTarget?.name}</span>? This
            cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.apiId)}
      />
    </div>
  )
}
