import { useMemo, useState } from "react"
import { useQuery, useMutation } from "@tanstack/react-query"
import { Trash2, Gauge } from "lucide-react"
import {
  usagePlansQueryOptions,
  usagePlanKeysQueryOptions,
  usagePlanUsageQueryOptions,
  apigwKeys,
  createUsagePlanMutationOptions,
  deleteUsagePlanMutationOptions,
  removeUsagePlanKeyMutationOptions,
} from "@/features/apigateway/data"
import { useEventStream } from "@/hooks/use-event-stream"
import { EventType } from "@/services/event-types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Spinner } from "@/components/ui/primitives"
import { CreateAction, RefreshAction, ResourceListPage } from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { useToast } from "@/components/ui/toast"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { ApiKeyValue } from "@/features/apigateway/components/api-key-value"
import type { UsagePlan, UsagePlanKey } from "@/features/apigateway/data"

// ─── Limit formatting ─────────────────────────────────────────────────────

/** "10/s (burst 20)" — the token bucket a plan's throttle describes. */
function formatThrottle(plan: UsagePlan): string {
  const rate = plan.throttle?.rateLimit
  const burst = plan.throttle?.burstLimit
  if (!rate && !burst) return "—"
  const parts: string[] = []
  if (rate) parts.push(`${rate}/s`)
  if (burst) parts.push(`burst ${burst}`)
  return parts.join(" · ")
}

/** "1000 / DAY" — the quota a plan configures, if any. */
function formatQuota(plan: UsagePlan): string {
  const limit = plan.quota?.limit
  if (!limit) return "—"
  return `${limit} / ${plan.quota?.period ?? "DAY"}`
}

/** Remaining is -1 when the plan sets no quota, and negative past an unenforced limit. */
function formatRemaining(remaining: number): string {
  return remaining < 0 && remaining === -1 ? "unlimited" : String(remaining)
}

// ─── Sub-component: throttle activity ─────────────────────────────────────

/**
 * Live feed of usage-plan limits being reached. Without this a 429 from the
 * emulator — or a limit that was measured but deliberately not enforced — is
 * indistinguishable from an application error.
 */
function ThrottleActivity({ planId }: { planId: string }) {
  const { events } = useEventStream({ source: "apigateway" })

  const throttles = useMemo(
    () =>
      events
        .filter((e) => e.type === EventType.apigateway.Throttled)
        .filter((e) => (e.payload as { usagePlanId?: string }).usagePlanId === planId)
        .slice(-8)
        .reverse(),
    [events, planId],
  )

  if (throttles.length === 0) {
    return (
      <p className="mt-3 text-xs text-fg-muted">
        No limits reached. Overcast measures every request against this plan; rejecting over-limit
        requests with a <span className="font-mono">429</span> additionally requires{" "}
        <span className="font-mono">OVERCAST_ENFORCE_APIGATEWAY_THROTTLE=true</span>.
      </p>
    )
  }

  return (
    <div className="mt-3 flex flex-col gap-1">
      <div className="text-sm font-medium text-fg-muted">Recent limit events</div>
      {throttles.map((event, i) => {
        const p = event.payload as {
          reason?: string
          enforced?: boolean
          apiKeyName?: string
          apiKeyId?: string
          used?: number
          remaining?: number
        }
        return (
          <div
            key={`${event.time}-${i}`}
            className="flex flex-wrap items-center gap-2 rounded-md border bg-bg px-2 py-1.5 text-xs"
          >
            <Badge variant={p.enforced ? "danger" : "warning"}>
              {p.enforced ? "429" : "report only"}
            </Badge>
            <span className="font-mono">{p.reason}</span>
            <span className="text-fg-muted">
              key {p.apiKeyName || p.apiKeyId} · used {p.used} · remaining{" "}
              {formatRemaining(p.remaining ?? -1)}
            </span>
            <span className="ml-auto text-fg-muted">
              {new Date(event.time).toLocaleTimeString()}
            </span>
          </div>
        )
      })}
    </div>
  )
}

// ─── Sub-component: keys for a selected plan ──────────────────────────────

function PlanKeys({ plan }: { plan: UsagePlan }) {
  const planId = plan.id
  const planName = plan.name
  const { toast } = useToast()
  const [removeTarget, setRemoveTarget] = useState<UsagePlanKey>()

  const { data: keys = [], isLoading, error } = useQuery(usagePlanKeysQueryOptions(planId))
  const { data: usage = [] } = useQuery(usagePlanUsageQueryOptions(planId))

  // GetUsage returns one [used, remaining] pair per day in the window, and the
  // window this page asks for is today only.
  const usageByKey = useMemo(() => {
    const out = new Map<string, { used: number; remaining: number }>()
    for (const row of usage) {
      if (row.days.length > 0) out.set(row.keyId, row.days[row.days.length - 1])
    }
    return out
  }, [usage])

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
      <div className="mb-3 flex flex-wrap items-center gap-2 text-sm font-medium text-fg-muted">
        <span>
          API Keys for <span className="font-semibold text-fg">{planName}</span>
        </span>
        <Badge variant="outline">
          <Gauge className="mr-1 h-3 w-3" />
          rate {formatThrottle(plan)}
        </Badge>
        <Badge variant="outline">quota {formatQuota(plan)}</Badge>
      </div>

      <ResourceTable
        variant="embedded"
        query={{ data: keys, isLoading: false, error }}
        noun="keys"
        emptyTitle="No keys associated with this plan."
        errorTitle="Failed to load usage plan keys"
        rowKey={(key) => key.id}
        columns={[
          {
            header: "Name",
            cellClassName: "font-medium",
            sortValue: (key) => key.name,
            cell: (key) => key.name,
          },
          { header: "ID", cellClassName: "text-fg-muted", cell: (key) => key.id },
          { header: "Value", cell: (key) => <ApiKeyValue value={key.value} /> },
          { header: "Type", cellClassName: "text-fg-muted", cell: (key) => key.type },
          {
            header: "Used today",
            sortValue: (key) => usageByKey.get(key.id)?.used ?? 0,
            cell: (key) => usageByKey.get(key.id)?.used ?? 0,
          },
          {
            header: "Remaining",
            cellClassName: "text-fg-muted",
            cell: (key) => formatRemaining(usageByKey.get(key.id)?.remaining ?? -1),
          },
        ]}
        rowActions={(key) => (
          <Button
            size="sm"
            variant="ghost"
            className="text-danger hover:text-danger"
            onClick={() => setRemoveTarget(key)}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      />

      <ThrottleActivity planId={planId} />

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
      toast({ title: "Usage plan deleted" })
    },
    onError: (err: Error) =>
      toast({ title: "Delete failed", description: err.message, variant: "danger" }),
  })

  return (
    <ResourceListPage
      title="Usage Plans"
      className="max-w-5xl"
      actions={
        <>
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create Usage Plan</CreateAction>
        </>
      }
    >
      {apiIdFilter ? (
        <div className="border-info/40 bg-info/10 text-info rounded-md border px-3 py-2 text-sm">
          Showing usage plans associated with API{" "}
          <span className="font-mono font-semibold">{apiIdFilter}</span>.{" "}
          <a className="underline underline-offset-2" href="/apigateway/usage-plans">
            Show all
          </a>
        </div>
      ) : null}

      <ResourceTable
        query={{ data: plans, isLoading, error }}
        noun="usage plans"
        emptyTitle="No usage plans yet"
        errorTitle="Failed to load usage plans"
        rowKey={(plan) => plan.id}
        // The plan's keys open under the plan. The chevron moved to the end of
        // the row when `ResourceTable` took ownership of expansion (#1327),
        // which is where every expandable table in the console now carries it.
        expandedContent={(plan) => <PlanKeys plan={plan} />}
        // A deep link names a plan; so does a filter that matches exactly one,
        // which is the "API key required" pill's landing state.
        defaultExpanded={(plan) =>
          plan.id === initialExpandedPlanId || (Boolean(apiIdFilter) && plans.length === 1)
        }
        columns={[
          {
            header: "Name",
            cellClassName: "font-medium",
            sortValue: (plan) => plan.name,
            cell: (plan) => plan.name,
          },
          { header: "ID", cellClassName: "text-fg-muted", cell: (plan) => plan.id },
          { header: "Description", prose: true, cell: (plan) => plan.description || "—" },
          {
            header: "Rate / burst",
            cellClassName: "font-mono text-fg-muted",
            cell: (plan) => formatThrottle(plan),
          },
          {
            header: "Quota",
            cellClassName: "font-mono text-fg-muted",
            cell: (plan) => formatQuota(plan),
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getVars: (plan) => plan.id,
          label: (plan) => plan.name,
          noun: "usage plan",
          title: "Delete Usage Plan",
          description: (plan) => (
            <>
              Delete usage plan <span className="font-mono font-semibold">{plan.name}</span>?
            </>
          ),
        }}
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
              <label className={cn(fieldLabel, "mb-1 block")} htmlFor="plan-name">
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
              <label className={cn(fieldLabel, "mb-1 block")} htmlFor="plan-description">
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
    </ResourceListPage>
  )
}
