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
import {
  CreateAction,
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import { CreateStackDialog } from "./create-stack-dialog"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"

interface StackListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function StackList({ sort, onSortChange }: StackListProps = {}) {
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

  const openStack = (stackName: string, stackId: string | undefined) =>
    navigate({
      to: "/cloudformation/$stackName",
      params: { stackName },
      search: { stackId },
    })

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
      <ResourceTable
        query={{ data: visibleStacks, isLoading, error }}
        noun="stacks"
        emptyIcon={Layers}
        emptyTitle="No stacks yet"
        emptyDescription="Deploy infrastructure by creating a CloudFormation stack from a template."
        emptyAction={
          <div className="flex flex-col items-center gap-4">
            <CreateAction onClick={() => setShowCreate(true)}>Create stack</CreateAction>
            {/* `ResourceTable` builds the empty state itself and has no slot for
                content beside the CTA, so the cross-region notice rides in the
                action slot. `[&>div]:mt-0` drops the negative top margin it uses
                to hug an `EmptyState` it follows as a sibling, and `empty:hidden`
                stops the wrapper reserving space on the ordinary "nothing
                anywhere" empty state, where the notice renders nothing at all. */}
            <div className="empty:hidden [&>div]:mt-0 [&>div]:mb-0">
              <RegionElsewhereNotice kind="cloudformation-stacks" noun="stacks" />
            </div>
          </div>
        }
        errorTitle="Failed to load stacks"
        // Four columns, none of them secondary — a columns menu here would
        // offer to hide things every row is read by.
        columnToggle={false}
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(stack) => stack.StackName ?? ""}
        onRowClick={(stack) => openStack(stack.StackName ?? "", stack.StackId)}
        columns={[
          {
            id: "name",
            header: "Stack name",
            sortValue: (stack) => stack.StackName,
            cell: (stack) => (
              <ResourceName icon={Layers} name={stack.StackName ?? ""}>
                {stack.ParentId && (
                  <Badge variant="outline" className="text-[10px] font-normal">
                    Nested
                  </Badge>
                )}
              </ResourceName>
            ),
          },
          {
            header: "Status",
            cell: (stack) => {
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
                <>
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
                </>
              )
            },
          },
          {
            id: "created",
            header: "Created",
            cellClassName: "text-fg-muted",
            sortValue: (stack) => stack.CreationTime,
            cell: (stack) => (stack.CreationTime ? stack.CreationTime.toLocaleString() : "—"),
          },
          {
            id: "updated",
            header: "Last updated",
            cellClassName: "text-fg-muted",
            sortValue: (stack) => stack.LastUpdatedTime,
            cell: (stack) => (stack.LastUpdatedTime ? stack.LastUpdatedTime.toLocaleString() : "—"),
          },
        ]}
        // Not `onDelete`: only some statuses can be deleted, and
        // `ResourceTable`'s delete config renders its action on every row.
        rowActions={(stack) => {
          const stackName = stack.StackName ?? ""
          return (
            <>
              <RowAction
                label={`View ${stackName}`}
                onClick={() => openStack(stackName, stack.StackId)}
              >
                <Eye className="h-3.5 w-3.5" />
              </RowAction>
              {canDeleteStack(stack.StackStatus ?? "") && (
                <RowAction
                  label={`Delete ${stackName}`}
                  tone="danger"
                  onClick={() => setDeleteTarget(stackName)}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </RowAction>
              )}
            </>
          )
        }}
      />

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
