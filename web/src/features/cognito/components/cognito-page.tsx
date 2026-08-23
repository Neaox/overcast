import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, UserCheck } from "lucide-react"
import {
  cognitoPoolsQueryOptions,
  cognitoKeys,
  deletePoolMutationOptions,
} from "@/features/cognito/data"
import type { CognitoPool } from "@/services/api/cognito"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  CreateAction,
  RefreshAction,
  ResourceListFilter,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreatePoolDialog } from "@/features/cognito/components/create-pool-dialog"
import { formatDate } from "@/lib/format"

interface CognitoPageProps {
  /** Current filter text — owned by the route's `q` search param, see `useFilterSearchParam`. */
  filter: string
  onFilterChange: (value: string) => void
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function CognitoPage({ filter, onFilterChange, sort, onSortChange }: CognitoPageProps) {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<CognitoPool>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: pools = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cognitoPoolsQueryOptions())

  const deleteMut = useResourceMutation({
    options: deletePoolMutationOptions(),
    invalidateKeys: [cognitoKeys.pools()],
    successTitle: "User pool deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? pools.filter(
            (p) =>
              p.name.toLowerCase().includes(filter.toLowerCase()) ||
              p.id.toLowerCase().includes(filter.toLowerCase()),
          )
        : pools,
    [pools, filter],
  )

  return (
    <ResourceListPage
      title="Cognito User Pools"
      count={pools.length}
      actions={
        <>
          <ServiceDocsButton
            service="cognito"
            label="Cognito"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create pool</CreateAction>
        </>
      }
    >
      <ResourceListFilter
        value={filter}
        onChange={onFilterChange}
        placeholder="Filter user pools…"
      />

      <ResourceTable
        query={{ data: filtered, isLoading, error }}
        noun="user pools"
        emptyIcon={UserCheck}
        emptyTitle="No user pools"
        emptyDescription="Create a user pool to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create pool</CreateAction>}
        errorTitle="Failed to load user pools"
        isFiltered={!!filter}
        onClearFilter={() => onFilterChange("")}
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(pool) => pool.id}
        onRowClick={(pool) => navigate({ to: "/cognito/$poolId", params: { poolId: pool.id } })}
        columns={[
          {
            header: "Name",
            sortValue: (pool) => pool.name,
            cell: (pool) => <ResourceName icon={UserCheck} name={pool.name} />,
          },
          {
            header: "Pool ID",
            cellClassName: "text-fg-muted",
            sortValue: (pool) => pool.id,
            cell: (pool) => pool.id,
          },
          {
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (pool) => pool.creationDate,
            cell: (pool) => formatDate(pool.creationDate),
          },
        ]}
        rowActions={(pool) => (
          <RowAction
            label={`View ${pool.name}`}
            onClick={() => navigate({ to: "/cognito/$poolId", params: { poolId: pool.id } })}
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
        )}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (pool) => pool.id,
          label: (pool) => pool.name,
          noun: "user pool",
          title: "Delete User Pool",
          description: (pool) => (
            <>
              Delete pool <span className="font-mono font-semibold">{pool.name}</span>? This cannot
              be undone.
            </>
          ),
        }}
      />

      <CreatePoolDialog open={showCreate} onOpenChange={setShowCreate} />
    </ResourceListPage>
  )
}
