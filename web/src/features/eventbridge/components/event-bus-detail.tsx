import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { CalendarClock, RefreshCw } from "lucide-react"
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
import { ResourceListFilter } from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
import { PageHeader } from "@/components/ui/primitives"
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
  const [filter, setFilter] = useState("")

  const {
    data: allRules = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ebRulesQueryOptions(busName))

  const [deleteTarget, setDeleteTarget] = useState<(typeof allRules)[number]>()

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
        <ResourceListFilter value={filter} onChange={setFilter} placeholder="Filter rules…" />

        <ResourceTable
          variant="embedded"
          query={{ data: filtered, isLoading, error }}
          noun="rules"
          emptyIcon={CalendarClock}
          emptyTitle="No rules"
          emptyDescription="No rules configured on this bus."
          errorTitle="Failed to load rules"
          filteredEmptyTitle="No rules"
          filteredEmptyDescription="No rules match the filter."
          isFiltered={!!filter}
          onClearFilter={() => setFilter("")}
          columnToggle={false}
          // ListRules returns the emulator's storage order; A→Z is what a name
          // column implies.
          defaultSort={{ id: "name", desc: false }}
          rowKey={(rule) => rule.Name ?? ""}
          columns={[
            {
              id: "name",
              header: "Name",
              sortValue: (rule) => rule.Name,
              cell: (rule) => rule.Name,
            },
            {
              header: "State",
              sortValue: (rule) => rule.State,
              cell: (rule) => (
                <Badge variant={rule.State === "ENABLED" ? "default" : "outline"}>
                  {rule.State}
                </Badge>
              ),
            },
            {
              id: "targets",
              header: "Targets & last delivery",
              cell: (rule) => {
                const targets = targetsByRule.get(rule.Name ?? "") ?? []
                return targets.length === 0 ? (
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
                )
              },
            },
            {
              header: "Description",
              prose: true,
              cell: (rule) => rule.Description || "—",
            },
          ]}
          onDelete={{
            target: deleteTarget,
            onRequest: setDeleteTarget,
            onOpenChange: (open) => !open && setDeleteTarget(undefined),
            mutation: deleteMut,
            getId: (rule) => rule.Name ?? "",
            label: (rule) => rule.Name ?? "rule",
            noun: "rule",
            title: "Delete Rule",
            description: (rule) => (
              <>
                Delete rule <span className="font-mono font-semibold">{rule.Name}</span>? This
                cannot be undone.
              </>
            ),
          }}
        />
      </section>
    </div>
  )
}
