import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { CalendarClock, Trash2, RefreshCw, Search } from "lucide-react"
import {
  ebRulesQueryOptions,
  ebRuleTargetsQueryOptions,
  ebDeliveriesQueryOptions,
  ebKeys,
  deleteRuleMutationOptions,
} from "@/features/eventbridge/data"
import type { EventDelivery, EventRuleTarget } from "@/services/api/eventbridge"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Badge } from "@/components/ui/badge"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { cn } from "@/lib/utils"

interface Props {
  busName: string
}

/**
 * How a delivery outcome reads in the UI. The emulator reports one of
 * delivered / retried / dlq / dropped per target (see
 * internal/services/eventbridge/deliveries.go).
 */
const OUTCOME_LABELS: Record<string, { label: string; variant: "success" | "warning" | "danger" }> =
  {
    delivered: { label: "delivered", variant: "success" },
    retried: { label: "retried", variant: "warning" },
    dlq: { label: "dead-lettered", variant: "warning" },
    dropped: { label: "dropped", variant: "danger" },
  }

/**
 * One target row: what it is, and what happened the last time the rule matched.
 * A target whose type is "Unknown" can never fire — the emulator refuses those
 * at PutTargets time, so seeing one means it predates that check.
 */
function TargetRow({ target, delivery }: { target: EventRuleTarget; delivery?: EventDelivery }) {
  const outcome = delivery ? OUTCOME_LABELS[delivery.Outcome] : undefined
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Badge variant={target.TargetType === "Unknown" ? "danger" : "accent"}>
        {target.TargetType}
      </Badge>
      <span className="font-mono text-xs text-fg-muted" title={target.Arn}>
        {target.Id}
      </span>
      {outcome ? (
        <Badge
          variant={outcome.variant}
          title={
            delivery?.Error
              ? `${delivery.Error} (after ${delivery.Attempts} attempt(s))`
              : `${delivery?.Attempts} attempt(s)`
          }
        >
          {outcome.label}
        </Badge>
      ) : (
        <Badge variant="outline" title="No delivery seen since the emulator started">
          no deliveries
        </Badge>
      )}
    </div>
  )
}

export function EventBusDetail({ busName }: Props) {
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")

  const {
    data: allRules = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ebRulesQueryOptions(busName))

  const { data: ruleTargets = [] } = useQuery(ebRuleTargetsQueryOptions(busName))
  const { data: deliveries = [] } = useQuery(ebDeliveriesQueryOptions(busName))

  // Targets by rule name, so the table can show what each rule fans out to.
  const targetsByRule = useMemo(() => {
    const map = new Map<string, EventRuleTarget[]>()
    for (const rule of ruleTargets) map.set(rule.Name, rule.Targets)
    return map
  }, [ruleTargets])

  // The most recent outcome per rule/target. The feed is newest-first, so the
  // first entry seen for a key is the one to show.
  const latestDelivery = useMemo(() => {
    const map = new Map<string, EventDelivery>()
    for (const d of deliveries) {
      const key = `${d.Rule}::${d.TargetId}`
      if (!map.has(key)) map.set(key, d)
    }
    return map
  }, [deliveries])

  const deleteMut = useResourceMutation({
    options: deleteRuleMutationOptions(),
    invalidateKeys: [ebKeys.rules()],
    successTitle: "Rule deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  // Filter rules to those belonging to this bus
  const rules = useMemo(
    () =>
      allRules.filter((r) => {
        const ruleBus = r.EventBusName || "default"
        return ruleBus === busName
      }),
    [allRules, busName],
  )

  const filtered = useMemo(
    () =>
      filter
        ? rules.filter((r) => (r.Name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : rules,
    [rules, filter],
  )

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={busName}
        description="Event bus rules and configuration"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
          </div>
        }
      />

      <ApplicationOwnershipBanner candidates={[busName]} />

      {/* Bus details card */}
      <DefinitionCard>
        <Definition label="Bus name" value={busName} />
        <Definition label="Rules" value={String(rules.length)} />
      </DefinitionCard>

      {/* Rules section */}
      <section className="flex flex-col gap-3">
        <h2 className="font-mono text-sm font-medium text-fg">Rules</h2>
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <Search className="absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2 text-fg-subtle" />
            <Input
              placeholder="Filter rules…"
              className="pl-8"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12">
            <Spinner className="h-5 w-5" />
          </div>
        ) : filtered.length === 0 ? (
          <EmptyState
            icon={<CalendarClock className="h-6 w-6 opacity-40" />}
            title="No rules"
            description={filter ? "No rules match the filter." : "No rules configured on this bus."}
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Targets &amp; last delivery</TableHead>
                <TableHead>Description</TableHead>
                <TableHead className="w-16" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((rule) => {
                const targets = targetsByRule.get(rule.Name ?? "") ?? []
                return (
                  <TableRow key={rule.Name}>
                    <TableCell>{rule.Name}</TableCell>
                    <TableCell>
                      <Badge variant={rule.State === "ENABLED" ? "default" : "outline"}>
                        {rule.State}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {targets.length === 0 ? (
                        <span className="text-xs text-fg-subtle">No targets</span>
                      ) : (
                        <div className="flex flex-col gap-1">
                          {targets.map((target) => (
                            <TargetRow
                              key={target.Id}
                              target={target}
                              delivery={latestDelivery.get(`${rule.Name}::${target.Id}`)}
                            />
                          ))}
                        </div>
                      )}
                    </TableCell>
                    <TableCellProse>{rule.Description || "—"}</TableCellProse>
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-danger hover:text-danger"
                        title="Delete"
                        onClick={() => setDeleteTarget(rule.Name)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </section>

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Rule"
        description={
          <>
            Delete rule <span className="font-mono font-semibold">{deleteTarget}</span>? This cannot
            be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}
