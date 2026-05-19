import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Zap, Plus, Trash2, RefreshCw } from "lucide-react"
import {
  lambdaFunctionsQueryOptions,
  lambdaKeys,
  deleteFunctionMutationOptions,
} from "@/features/lambda/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
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
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
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

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Lambda Functions"
        description="Manage Lambda function definitions."
        actions={
          <div className="flex gap-2">
            <ServiceDocsButton
              service="lambda"
              label="Lambda"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" disabled={isFetching} onClick={() => refetch()}>
              <RefreshCw className={cn("mr-1 h-4 w-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create function
            </Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : functions.length === 0 ? (
        <EmptyState
          icon={<Zap className="h-8 w-8 opacity-40" />}
          title="No functions"
          description="Create a function to get started."
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
              <TableHead className="w-16" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {functions.map((fn) => (
              <FunctionRow key={fn.FunctionArn} fn={fn} onDelete={setDeleteTarget} />
            ))}
          </TableBody>
        </Table>
      )}

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
    </div>
  )
}

// ─── FunctionRow ─────────────────────────────────────────────────────────────

function FunctionRow({ fn, onDelete }: { fn: LambdaFunction; onDelete: (name: string) => void }) {
  const navigate = useNavigate()

  return (
    <TableRow
      className="cursor-pointer"
      onClick={() => navigate({ to: "/lambda/$name", params: { name: fn.FunctionName ?? "" } })}
    >
      <TableCell className="font-mono font-medium">{fn.FunctionName}</TableCell>
      <TableCell className="text-sm text-fg-muted">{fn.Runtime}</TableCell>
      <TableCell className="font-mono text-xs text-fg-muted">{fn.Handler}</TableCell>
      <TableCell className="text-sm text-fg-muted">{fn.MemorySize ?? 128} MB</TableCell>
      <TableCell className="text-sm text-fg-muted">{fn.Timeout ?? 3}s</TableCell>
      <TableCell>
        <span
          className={cn(
            "text-xs",
            fn.State === "Active" ? "font-medium text-green-500" : "text-fg-muted",
          )}
        >
          {fn.State}
        </span>
      </TableCell>
      <TableCell>
        <Button
          variant="ghost"
          size="sm"
          className="text-danger hover:text-danger"
          onClick={(e) => {
            e.stopPropagation()
            onDelete(fn.FunctionName ?? "")
          }}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </TableCell>
    </TableRow>
  )
}
