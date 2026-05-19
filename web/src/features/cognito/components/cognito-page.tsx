import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { UserCheck, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  cognitoPoolsQueryOptions,
  cognitoKeys,
  deletePoolMutationOptions,
} from "@/features/cognito/data"
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
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreatePoolDialog } from "@/features/cognito/components/create-pool-dialog"
import { formatDate } from "@/lib/format"
import { cn } from "@/lib/utils"

export function CognitoPage() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ id: string; name: string }>()
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const { data: pools = [], isLoading, isFetching, refetch } = useQuery(cognitoPoolsQueryOptions())

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
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Cognito"
        description="User pools and identity management"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="cognito"
              label="Cognito"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create pool
            </Button>
          </div>
        }
      />

      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter user pools…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<UserCheck className="h-8 w-8 opacity-40" />}
          title="No user pools"
          description={filter ? "No pools match the filter." : "Create a user pool to get started."}
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create pool
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Pool ID</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((pool) => (
              <TableRow
                key={pool.id}
                className="cursor-pointer"
                onClick={() => navigate({ to: "/cognito/$poolId", params: { poolId: pool.id } })}
              >
                <TableCell className="font-medium">{pool.name}</TableCell>
                <TableCell className="font-mono text-sm text-fg-muted">{pool.id}</TableCell>
                <TableCell className="text-sm text-fg-muted">
                  {formatDate(pool.creationDate)}
                </TableCell>
                <TableCell>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-danger hover:text-danger"
                    title="Delete"
                    onClick={(e) => {
                      e.stopPropagation()
                      setDeleteTarget({ id: pool.id, name: pool.name })
                    }}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

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
    </div>
  )
}
