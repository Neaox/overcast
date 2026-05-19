import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Layers, Plus, RefreshCw, Trash2, Loader2 } from "lucide-react"
import {
  cfnStacksQueryOptions,
  cfnKeys,
  deleteStackMutationOptions,
} from "@/features/cloudformation/data"
import {
  stackStatusVariant,
  canDeleteStack,
  formatStatus,
  isStackInProgress,
} from "@/features/cloudformation/utils"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { EmptyState, PageHeader, QueryListState } from "@/components/ui/primitives"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { CreateStackDialog } from "./create-stack-dialog"
import { cn } from "@/lib/utils"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

export function StackList() {
  const navigate = useNavigate()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: stacks = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cfnStacksQueryOptions())

  // Filter out deleted stacks (AWS ListStacks includes DELETE_COMPLETE by default)
  const visibleStacks = stacks.filter((s) => s.StackStatus !== "DELETE_COMPLETE")

  const deleteMut = useResourceMutation({
    options: deleteStackMutationOptions(),
    invalidateKeys: [cfnKeys.stacks()],
    successTitle: "Stack deleted",
    successDescription: (name) => name,
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="CloudFormation Stacks"
        description={
          isLoading
            ? undefined
            : `${visibleStacks.length} stack${visibleStacks.length !== 1 ? "s" : ""}`
        }
        actions={
          <>
            <ServiceDocsButton
              service="cloudformation"
              label="CloudFormation"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              size="sm"
              variant="secondary"
              onClick={() => void refetch()}
              disabled={isFetching}
            >
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create stack
            </Button>
          </>
        }
      />

      {isLoading || visibleStacks.length === 0 ? (
        <QueryListState
          isLoading={isLoading}
          isEmpty={visibleStacks.length === 0}
          error={error}
          empty={
            <EmptyState
              icon={<Layers className="h-10 w-10" />}
              title="No stacks yet"
              description="Deploy infrastructure by creating a CloudFormation stack from a template."
              action={
                <Button size="sm" onClick={() => setShowCreate(true)}>
                  <Plus className="mr-1.5 h-3.5 w-3.5" />
                  Create stack
                </Button>
              }
            />
          }
          errorTitle="Failed to load stacks"
        />
      ) : (
        <div className="rounded-md border border-border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Stack name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Last updated</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleStacks.map((stack) => (
                <TableRow
                  key={stack.StackName}
                  className="cursor-pointer"
                  onClick={() =>
                    navigate({
                      to: "/cloudformation/$stackName",
                      params: { stackName: stack.StackName ?? "" },
                    })
                  }
                >
                  <TableCell className="font-mono text-sm font-medium">
                    <span className="flex items-center gap-1.5">
                      {stack.StackName}
                      {stack.ParentId && (
                        <Badge variant="outline" className="text-[10px] font-normal">
                          Nested
                        </Badge>
                      )}
                    </span>
                  </TableCell>
                  <TableCell>
                    <span className="flex items-center gap-1.5">
                      {isStackInProgress(stack.StackStatus ?? "") && (
                        <Loader2 className="h-3 w-3 animate-spin text-fg-muted" />
                      )}
                      <Badge variant={stackStatusVariant(stack.StackStatus ?? "")}>
                        {formatStatus(stack.StackStatus ?? "")}
                      </Badge>
                    </span>
                  </TableCell>
                  <TableCell className="text-sm text-fg-muted">
                    {stack.CreationTime ? stack.CreationTime.toLocaleString() : "—"}
                  </TableCell>
                  <TableCell className="text-sm text-fg-muted">
                    {stack.LastUpdatedTime ? stack.LastUpdatedTime.toLocaleString() : "—"}
                  </TableCell>
                  <TableCell className="text-right">
                    {canDeleteStack(stack.StackStatus ?? "") && (
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        title="Delete stack"
                        onClick={(e) => {
                          e.stopPropagation()
                          setDeleteTarget(stack.StackName ?? "")
                        }}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <CreateStackDialog open={showCreate} onOpenChange={setShowCreate} />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete stack"
        description={
          <>
            Permanently delete <strong>{deleteTarget}</strong> and all its resources?
          </>
        }
        confirmLabel="Delete stack"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
