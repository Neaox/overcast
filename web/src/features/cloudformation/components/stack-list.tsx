import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Eye, Layers, Trash2, Loader2 } from "lucide-react"
import {
  cfnStacksQueryOptions,
  cfnKeys,
  deleteStackMutationOptions,
} from "@/features/cloudformation/data"
import {
  stackStatusVariant,
  canDeleteStack,
  formatStatus,
  isStackFailed,
  isStackInProgress,
  isStackRollingBack,
  stackStatusExplanation,
} from "@/features/cloudformation/utils"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Badge } from "@/components/ui/badge"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import { CreateStackDialog } from "./create-stack-dialog"
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

  const inProgressCount = visibleStacks.filter((s) => isStackInProgress(s.StackStatus ?? "")).length

  return (
    <ResourceListPage
      title="CloudFormation Stacks"
      count={isLoading ? undefined : visibleStacks.length}
      meta={inProgressCount > 0 ? `${inProgressCount} in progress` : undefined}
      actions={
        <>
          <ServiceDocsButton
            service="cloudformation"
            label="CloudFormation"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => void refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create stack</CreateAction>
        </>
      }
    >
      <ResourceListCard>
        {isLoading || visibleStacks.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={visibleStacks.length === 0}
            error={error}
            empty={
              <>
                <EmptyState
                  icon={<Layers className="h-10 w-10" />}
                  title="No stacks yet"
                  description="Deploy infrastructure by creating a CloudFormation stack from a template."
                  action={
                    <CreateAction onClick={() => setShowCreate(true)}>Create stack</CreateAction>
                  }
                />
                <RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />
              </>
            }
            errorTitle="Failed to load stacks"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Stack name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Last updated</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {visibleStacks.map((stack) => {
                const stackName = stack.StackName ?? ""
                const stackStatus = stack.StackStatus ?? ""
                // A red badge says a deploy failed; this says why, so the list
                // answers the question without a click. The reason comes from
                // the StackStatusReason on the ListStacks summary, falling
                // back to the standing meaning of the status for the terminal
                // rollback states, which clear it.
                const failureNote =
                  isStackFailed(stackStatus) || isStackRollingBack(stackStatus)
                    ? (stack.StackStatusReason ?? stackStatusExplanation(stackStatus))
                    : undefined
                return (
                  <TableRow
                    key={stackName}
                    onClick={() =>
                      navigate({
                        to: "/cloudformation/$stackName",
                        params: { stackName },
                      })
                    }
                  >
                    <TableCell>
                      <ResourceName icon={Layers} name={stackName}>
                        {stack.ParentId && (
                          <Badge variant="outline" className="text-[10px] font-normal">
                            Nested
                          </Badge>
                        )}
                      </ResourceName>
                    </TableCell>
                    <TableCell>
                      <span className="flex items-center gap-1.5">
                        {isStackInProgress(stackStatus) && (
                          <Loader2 className="h-3 w-3 animate-spin text-fg-muted" />
                        )}
                        <Badge variant={stackStatusVariant(stackStatus)}>
                          {formatStatus(stackStatus)}
                        </Badge>
                      </span>
                      {failureNote && (
                        <p className="mt-0.5 max-w-md font-sans text-[13px] text-danger">
                          {failureNote}
                        </p>
                      )}
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {stack.CreationTime ? stack.CreationTime.toLocaleString() : "—"}
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {stack.LastUpdatedTime ? stack.LastUpdatedTime.toLocaleString() : "—"}
                    </TableCell>
                    <TableCell onClick={(e) => e.stopPropagation()}>
                      <RowActions>
                        <RowAction
                          label={`View ${stackName}`}
                          onClick={() =>
                            navigate({ to: "/cloudformation/$stackName", params: { stackName } })
                          }
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </RowAction>
                        {canDeleteStack(stackStatus) && (
                          <RowAction
                            label={`Delete ${stackName}`}
                            tone="danger"
                            onClick={() => setDeleteTarget(stackName)}
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </RowAction>
                        )}
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

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
    </ResourceListPage>
  )
}
