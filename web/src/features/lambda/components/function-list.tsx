import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { Eye, Zap } from "lucide-react"
import {
  lambdaFunctionsQueryOptions,
  lambdaKeys,
  deleteFunctionMutationOptions,
} from "@/features/lambda/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import type { LambdaFunction } from "@/types"
import { RegionElsewhereNotice } from "@/features/preflight/components/region-elsewhere-notice"
import {
  CreateAction,
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { CreateFunctionWizard } from "./create-wizard"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { cn } from "@/lib/utils"

// ─── Component ───────────────────────────────────────────────────────────────

export function FunctionList() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<LambdaFunction>()
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
    successDescription: ({ name }) => name,
    successVariant: "default",
    errorTitle: "Delete failed",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const activeCount = functions.filter((fn) => fn.State === "Active").length
  const isEmpty = !isLoading && !error && functions.length === 0

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
      {/*
       * `variant="embedded"` inside this page's own `ResourceListCard` rather
       * than `variant="card"`: `RegionElsewhereNotice` has to render directly
       * beneath the empty state, inside the card (its `-mt-8` tucks it into the
       * `EmptyState`'s bottom padding), and `ResourceTable` builds that empty
       * state internally with no slot after it. Embedding the table in the card
       * this page owns is what keeps the notice where it reads. The columns menu
       * is off for the same reason — inside the card its popover is clipped by
       * the `overflow-hidden` that rounds the table's corners.
       */}
      <ResourceListCard>
        <ResourceTable
          variant="embedded"
          columnToggle={false}
          query={{ data: functions, isLoading, error }}
          noun="functions"
          emptyIcon={Zap}
          emptyTitle="No functions yet"
          emptyDescription="Create a function to get started."
          emptyAction={
            <CreateAction onClick={() => setShowCreate(true)}>Create function</CreateAction>
          }
          errorTitle="Failed to load functions"
          rowKey={(fn) => fn.FunctionArn ?? fn.FunctionName ?? ""}
          onRowClick={(fn) =>
            navigate({ to: "/lambda/$name", params: { name: fn.FunctionName ?? "" } })
          }
          columns={[
            {
              header: "Name",
              sortValue: (fn) => fn.FunctionName,
              cell: (fn) => <ResourceName icon={Zap} name={fn.FunctionName ?? ""} />,
            },
            { header: "Runtime", cellClassName: "text-fg-muted", cell: (fn) => fn.Runtime },
            { header: "Handler", cellClassName: "text-fg-muted", cell: (fn) => fn.Handler },
            {
              header: "Memory",
              cellClassName: "text-fg-muted",
              sortValue: (fn) => fn.MemorySize ?? 128,
              cell: (fn) => `${fn.MemorySize ?? 128} MB`,
            },
            {
              header: "Timeout",
              cellClassName: "text-fg-muted",
              sortValue: (fn) => fn.Timeout ?? 3,
              cell: (fn) => `${fn.Timeout ?? 3}s`,
            },
            {
              header: "State",
              cell: (fn) => (
                <span
                  className={cn(
                    "font-mono text-xs",
                    fn.State === "Active" ? "font-medium text-success" : "text-fg-muted",
                  )}
                >
                  {fn.State}
                </span>
              ),
            },
          ]}
          rowActions={(fn) => (
            <RowAction
              label={`View ${fn.FunctionName ?? ""}`}
              onClick={() =>
                navigate({ to: "/lambda/$name", params: { name: fn.FunctionName ?? "" } })
              }
            >
              <Eye className="h-3.5 w-3.5" />
            </RowAction>
          )}
          onDelete={{
            target: deleteTarget,
            onRequest: setDeleteTarget,
            onOpenChange: (open) => !open && setDeleteTarget(undefined),
            mutation: {
              mutate: (name) => deleteMut.mutate({ name }),
              isPending: deleteMut.isPending,
            },
            getId: (fn) => fn.FunctionName ?? "",
            label: (fn) => fn.FunctionName ?? "",
            noun: "function",
            title: "Delete function",
            description: (fn) => (
              <>
                Delete <span className="font-mono font-medium">{fn.FunctionName}</span>, including
                every published version and alias? This cannot be undone. To remove a single
                version, use the function's Versions tab.
              </>
            ),
          }}
        />
        {isEmpty && <RegionElsewhereNotice kind="lambda-functions" noun="functions" />}
      </ResourceListCard>

      <CreateFunctionWizard open={showCreate} onOpenChange={setShowCreate} />
    </ResourceListPage>
  )
}
