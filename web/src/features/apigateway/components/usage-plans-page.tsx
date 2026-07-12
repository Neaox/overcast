import { useState } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import { Plus, Trash2, RefreshCw, ChevronRight } from "lucide-react"
import {
  usagePlansQueryOptions,
  usagePlanKeysQueryOptions,
  apigwKeys,
  createUsagePlanMutationOptions,
  deleteUsagePlanMutationOptions,
  removeUsagePlanKeyMutationOptions,
} from "@/features/apigateway/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, QueryListState, Spinner } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"
import { cn } from "@/lib/utils"
import { ApiKeyValue } from "@/features/apigateway/components/api-key-value"
import type { UsagePlan, UsagePlanKey } from "@/features/apigateway/data"

// ─── Sub-component: keys for a selected plan ──────────────────────────────

function PlanKeys({ planId, planName }: { planId: string; planName: string }) {
  const { toast } = useToast()
  const [removeTarget, setRemoveTarget] = useState<UsagePlanKey>()

  const { data: keys = [], isLoading, error } = useQuery(usagePlanKeysQueryOptions(planId))

  const removeMut = useMutation({
    ...removeUsagePlanKeyMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.usagePlanKeys(planId) })
      setRemoveTarget(undefined)
      toast({ title: "Key removed from plan" })
    },
    onError: (err: Error) =>
      toast({ title: "Remove failed", description: err.message, variant: "danger" }),
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-4">
        <Spinner className="h-4 w-4" />
      </div>
    )
  }

  return (
    <div className="mt-2 rounded-lg border bg-bg-elevated p-4">
      <div className="mb-3 text-sm font-medium text-fg-muted">
        API Keys for <span className="font-semibold text-fg">{planName}</span>
      </div>
      {keys.length === 0 ? (
        <QueryListState
          isLoading={isLoading}
          isEmpty
          error={error}
          loadingClassName="py-4"
          emptyTitle="No keys associated with this plan."
          emptyClassName="py-4"
          errorTitle="Failed to load usage plan keys"
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>Value</TableHead>
              <TableHead>Type</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {keys.map((key) => (
              <TableRow key={key.id}>
                <TableCell className="font-medium">{key.name}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{key.id}</TableCell>
                <TableCell>
                  <ApiKeyValue value={key.value} />
                </TableCell>
                <TableCell className="text-sm text-fg-muted">{key.type}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setRemoveTarget(key)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <ConfirmDialog
        open={!!removeTarget}
        onOpenChange={(open) => !open && setRemoveTarget(undefined)}
        title="Remove Key from Plan"
        description={
          <>
            Remove key <span className="font-mono font-semibold">{removeTarget?.name}</span> from
            this usage plan?
          </>
        }
        isPending={removeMut.isPending}
        onConfirm={() => removeTarget && removeMut.mutate({ planId, keyId: removeTarget.id })}
      />
    </div>
  )
}

// ─── Main page ────────────────────────────────────────────────────────────

export function UsagePlansPage({
  apiIdFilter,
  initialExpandedPlanId,
}: {
  apiIdFilter?: string
  initialExpandedPlanId?: string
} = {}) {
  const { toast } = useToast()

  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<UsagePlan>()
  const [expandedPlanId, setExpandedPlanId] = useState<string | undefined>(initialExpandedPlanId)

  // Form state
  const [newPlanName, setNewPlanName] = useState("")
  const [newPlanDescription, setNewPlanDescription] = useState("")

  const {
    data: allPlans = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(usagePlansQueryOptions())

  // Filter by API id when the URL provides one — used when deep-linking
  // from a method's "API key required" pill.
  const plans = apiIdFilter
    ? allPlans.filter((p) => (p.apiStages ?? []).some((s) => s.apiId === apiIdFilter))
    : allPlans

  // When filtering and exactly one plan matches, auto-expand it.
  if (apiIdFilter && !expandedPlanId && plans.length === 1) {
    // Defer to next render via state setter to avoid setState during render.
    queueMicrotask(() => setExpandedPlanId(plans[0].id))
  }

  const createMut = useMutation({
    ...createUsagePlanMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.usagePlans() })
      setShowCreate(false)
      setNewPlanName("")
      setNewPlanDescription("")
      toast({ title: "Usage plan created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const deleteMut = useMutation({
    ...deleteUsagePlanMutationOptions(),
    onSuccess: (_data, _variables, _result, { client }) => {
      void client.invalidateQueries({ queryKey: apigwKeys.usagePlans() })
      setDeleteTarget(undefined)
      if (expandedPlanId === deleteTarget?.id) setExpandedPlanId(undefined)
      toast({ title: "Usage plan deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  function togglePlan(planId: string) {
    setExpandedPlanId((prev) => (prev === planId ? undefined : planId))
  }

  return (
    <div className="flex w-full max-w-5xl flex-col gap-4">
      <PageHeader
        title="Usage Plans"
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => void refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Usage Plan
            </Button>
          </>
        }
      />

      {apiIdFilter ? (
        <div className="border-info/40 bg-info/10 text-info rounded-md border px-3 py-2 text-sm">
          Showing usage plans associated with API{" "}
          <span className="font-mono font-semibold">{apiIdFilter}</span>.{" "}
          <a className="underline underline-offset-2" href="/apigateway/usage-plans">
            Show all
          </a>
        </div>
      ) : null}

      {isLoading || plans.length === 0 ? (
        <QueryListState
          isLoading={isLoading}
          isEmpty={plans.length === 0}
          error={error}
          emptyTitle="No usage plans yet"
          errorTitle="Failed to load usage plans"
        />
      ) : (
        <div className="flex flex-col gap-2">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead />
                <TableHead>Name</TableHead>
                <TableHead>ID</TableHead>
                <TableHead>Description</TableHead>
                <TableHead />
              </TableRow>
            </TableHeader>
            <TableBody>
              {plans.map((plan) => (
                <TableRow
                  key={plan.id}
                  className="hover:bg-muted/50 cursor-pointer"
                  onClick={() => togglePlan(plan.id)}
                >
                  <TableCell className="w-8">
                    <ChevronRight
                      className={cn(
                        "h-4 w-4 text-fg-muted transition-transform",
                        expandedPlanId === plan.id && "rotate-90",
                      )}
                    />
                  </TableCell>
                  <TableCell className="font-medium">{plan.name}</TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">{plan.id}</TableCell>
                  <TableCell className="text-sm text-fg-muted">{plan.description || "—"}</TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-danger hover:text-danger"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDeleteTarget(plan)
                      }}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          {expandedPlanId && (
            <PlanKeys
              planId={expandedPlanId}
              planName={plans.find((p) => p.id === expandedPlanId)?.name ?? expandedPlanId}
            />
          )}
        </div>
      )}

      {/* Delete confirmation */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Usage Plan"
        description={
          <>
            Delete usage plan <span className="font-mono font-semibold">{deleteTarget?.name}</span>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.id)}
      />

      {/* Create dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(v) => {
          if (!v) {
            setShowCreate(false)
            setNewPlanName("")
            setNewPlanDescription("")
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create Usage Plan</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              createMut.mutate({
                name: newPlanName,
                description: newPlanDescription || undefined,
              })
            }}
            className="flex flex-col gap-4"
          >
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="plan-name">
                Name <span className="text-danger">*</span>
              </label>
              <input
                id="plan-name"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newPlanName}
                onChange={(e) => setNewPlanName(e.target.value)}
                placeholder="my-plan"
                autoFocus
                required
              />
            </div>
            <div>
              <label className="mb-1 block text-sm font-medium" htmlFor="plan-description">
                Description
              </label>
              <input
                id="plan-description"
                className="w-full rounded-md border bg-bg-elevated px-3 py-2 text-sm"
                value={newPlanDescription}
                onChange={(e) => setNewPlanDescription(e.target.value)}
                placeholder="Optional description"
              />
            </div>
            <DialogFooter>
              <Button variant="ghost" type="button" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={!newPlanName || createMut.isPending}>
                {createMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
