import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Eye, Zap, Trash2 } from "lucide-react"
import {
  lambdaFunctionsQueryOptions,
  lambdaKeys,
  deleteFunctionMutationOptions,
} from "@/features/lambda/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import type { LambdaFunction } from "@/types"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { QueryListState, EmptyState } from "@/components/ui/primitives"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"
import { CreateFunctionWizard } from "./create-wizard"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { cn } from "@/lib/utils"

// ─── Component ───────────────────────────────────────────────────────────────

export function FunctionList() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: functions = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(lambdaFunctionsQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteFunctionMutationOptions(),
    invalidateKeys: [lambdaKeys.functions()],
    successTitle: "Function deleted",
    successDescription: (name) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const activeCount = functions.filter((fn) => fn.State === "Active").length

  return (
    <ResourceListPage
      title="Lambda Functions"
      count={functions.length}
      meta={functions.length > 0 ? `${activeCount} active` : undefined}
      actions={
        <>
          <ServiceDocsButton
            service="lambda"
            label="Lambda"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create function</CreateAction>
        </>
      }
    >
      <ResourceListCard>
        {isLoading || functions.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={functions.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Zap className="h-10 w-10" />}
                title="No functions yet"
                description="Create a function to get started."
                action={
                  <CreateAction onClick={() => setShowCreate(true)}>Create function</CreateAction>
                }
              />
            }
            errorTitle="Failed to load functions"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Runtime</TableHead>
                <TableHead>Handler</TableHead>
                <TableHead>Memory</TableHead>
                <TableHead>Timeout</TableHead>
                <TableHead>State</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {functions.map((fn) => (
                <FunctionRow key={fn.FunctionArn} fn={fn} onDelete={setDeleteTarget} />
              ))}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      <CreateFunctionWizard open={showCreate} onOpenChange={setShowCreate} />

      {/* Delete confirmation dialog */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={() => setDeleteTarget(undefined)}
        title="Delete function"
        description={
          <>
            Delete <span className="font-mono font-medium">{deleteTarget}</span>? This cannot be
            undone.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </ResourceListPage>
  )
}

// ─── FunctionRow ─────────────────────────────────────────────────────────────

function FunctionRow({ fn, onDelete }: { fn: LambdaFunction; onDelete: (name: string) => void }) {
  const navigate = useNavigate()
  const name = fn.FunctionName ?? ""

  return (
    <TableRow onClick={() => navigate({ to: "/lambda/$name", params: { name } })}>
      <TableCell>
        <ResourceName icon={Zap} name={name} />
      </TableCell>
      <TableCell className="text-fg-muted">{fn.Runtime}</TableCell>
      <TableCell className="text-fg-muted">{fn.Handler}</TableCell>
      <TableCell className="text-fg-muted">{fn.MemorySize ?? 128} MB</TableCell>
      <TableCell className="text-fg-muted">{fn.Timeout ?? 3}s</TableCell>
      <TableCell>
        <span
          className={cn(
            "font-mono text-xs",
            fn.State === "Active" ? "font-medium text-success" : "text-fg-muted",
          )}
        >
          {fn.State}
        </span>
      </TableCell>
      <TableCell onClick={(e) => e.stopPropagation()}>
        <RowActions>
          <RowAction
            label={`View ${name}`}
            onClick={() => navigate({ to: "/lambda/$name", params: { name } })}
          >
            <Eye className="h-3.5 w-3.5" />
          </RowAction>
          <RowAction label={`Delete ${name}`} tone="danger" onClick={() => onDelete(name)}>
            <Trash2 className="h-3.5 w-3.5" />
          </RowAction>
        </RowActions>
      </TableCell>
    </TableRow>
  )
}
