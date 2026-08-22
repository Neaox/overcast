import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, UserCheck, Trash2 } from "lucide-react"
import {
  cognitoPoolsQueryOptions,
  cognitoKeys,
  deletePoolMutationOptions,
} from "@/features/cognito/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { QueryListState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListFilter,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreatePoolDialog } from "@/features/cognito/components/create-pool-dialog"
import { formatDate } from "@/lib/format"

interface CognitoPageProps {
  /** Current filter text — owned by the route's `q` search param, see `useFilterSearchParam`. */
  filter: string
  onFilterChange: (value: string) => void
}

export function CognitoPage({ filter, onFilterChange }: CognitoPageProps) {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string }>()
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

      <ResourceListCard>
        {isLoading || filtered.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={filtered.length === 0}
            error={error}
            emptyIcon={<UserCheck className="h-10 w-10" />}
            emptyTitle="No user pools"
            emptyDescription="Create a user pool to get started."
            emptyAction={
              <CreateAction onClick={() => setShowCreate(true)}>Create pool</CreateAction>
            }
            isFiltered={!!filter}
            onClearFilter={() => onFilterChange("")}
            errorTitle="Failed to load user pools"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Pool ID</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((pool) => (
                <TableRow
                  key={pool.id}
                  onClick={() => navigate({ to: "/cognito/$poolId", params: { poolId: pool.id } })}
                >
                  <TableCell>
                    <ResourceName icon={UserCheck} name={pool.name} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{pool.id}</TableCell>
                  <TableCell className="text-fg-muted">{formatDate(pool.creationDate)}</TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RowActions>
                      <RowAction
                        label={`View ${pool.name}`}
                        onClick={() =>
                          navigate({ to: "/cognito/$poolId", params: { poolId: pool.id } })
                        }
                      >
                        <Eye className="h-3.5 w-3.5" />
                      </RowAction>
                      <RowAction
                        label={`Delete ${pool.name}`}
                        tone="danger"
                        onClick={() => setDeleteTarget({ id: pool.id, name: pool.name })}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </RowAction>
                    </RowActions>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      <CreatePoolDialog open={showCreate} onOpenChange={setShowCreate} />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete User Pool"
        description={
          <>
            Delete pool <span className="font-mono font-semibold">{deleteTarget?.name}</span>? This
            cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />
    </ResourceListPage>
  )
}
