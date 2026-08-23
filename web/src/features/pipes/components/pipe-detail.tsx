import { useQuery } from "@tanstack/react-query"
import { ArrowRight, GitBranch, RefreshCw, Sparkles, Target } from "lucide-react"
import {
  pipeQueryOptions,
  pipeWiringQueryOptions,
  pipeDeliveriesQueryOptions,
} from "@/features/pipes/data"
import type { PipeWiring } from "@/services/api/pipes"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Definition, DefinitionCard } from "@/components/ui/definition-card"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ResourceTable } from "@/components/ui/resource-table"
import { cn } from "@/lib/utils"

interface Props {
  pipeName: string
}

/**
 * How an execution outcome reads in the UI. The emulator reports one of
 * delivered / filtered / failed per batch (see
 * internal/services/pipes/deliveries.go).
 */
const OUTCOME_LABELS: Record<
  string,
  { label: string; variant: "success" | "warning" | "danger" } | undefined
> = {
  delivered: { label: "delivered", variant: "success" },
  filtered: { label: "dropped by enrichment", variant: "warning" },
  failed: { label: "failed", variant: "danger" },
  dlq: { label: "dead-lettered", variant: "warning" },
}

/**
 * One leg of the pipe: what it is, and the ARN behind it. A leg whose type is
 * "Unknown" is one the emulator cannot resolve, which is what makes the whole
 * pipe inert.
 */
function Leg({
  icon: Icon,
  label,
  type,
  arn,
}: {
  icon: React.ElementType
  label: string
  type: string
  arn: string
}) {
  const unknown = type === "Unknown"
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1.5 rounded-lg border border-border bg-bg-subtle p-3">
      <span className="flex items-center gap-1.5 text-xs font-medium text-fg-muted">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </span>
      <Badge variant={unknown ? "danger" : "accent"}>{type}</Badge>
      <span className="truncate font-mono text-xs text-fg-muted" title={arn}>
        {arn}
      </span>
    </div>
  )
}

/** State badge tone, matching the list page. */
function stateVariant(state: string): "default" | "success" | "warning" | "danger" | "accent" {
  switch (state) {
    case "RUNNING":
      return "success"
    case "CREATING":
    case "UPDATING":
    case "STARTING":
      return "accent"
    case "DELETING":
    case "STOPPING":
      return "warning"
    case "STOPPED":
      return "danger"
    default:
      return "default"
  }
}

export function PipeDetail({ pipeName }: Props) {
  const { data: pipe, isLoading, isFetching, refetch } = useQuery(pipeQueryOptions(pipeName))
  const { data: allWiring = [], refetch: refetchWiring } = useQuery(pipeWiringQueryOptions())
  const { data: deliveries = [], refetch: refetchDeliveries } = useQuery(
    pipeDeliveriesQueryOptions(pipeName),
  )

  const wiring: PipeWiring | undefined = allWiring.find((w) => w.Name === pipeName)

  function refreshAll() {
    void refetch()
    void refetchWiring()
    void refetchDeliveries()
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={pipeName}
        description="Pipe source, enrichment and target"
        actions={
          <Button
            variant="ghost"
            size="sm"
            onClick={refreshAll}
            disabled={isFetching}
            title="Refresh"
          >
            <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
          </Button>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-5 w-5" />
        </div>
      ) : (
        <>
          <DefinitionCard>
            <Definition label="Name" value={pipeName} />
            <Definition
              label="Current state"
              value={
                <Badge variant={stateVariant(pipe?.CurrentState ?? "")}>{pipe?.CurrentState}</Badge>
              }
            />
            <Definition label="Desired state" value={pipe?.DesiredState ?? "—"} />
            <Definition label="ARN" value={pipe?.Arn ?? "—"} full valueClassName="font-mono" />
            <Definition label="Role" value={pipe?.RoleArn ?? "—"} full valueClassName="font-mono" />
            <Definition label="Description" value={pipe?.Description || "—"} />
          </DefinitionCard>

          {/*
            A pipe whose wiring the emulator cannot run would otherwise sit here
            reading RUNNING for ever. Say so instead — this is the whole reason
            the detail view exists.
          */}
          {wiring && !wiring.Wired && (
            <div className="rounded-lg border border-danger/40 bg-danger/10 p-3 text-sm text-fg">
              <p className="font-medium">Stored, but not running</p>
              <p className="mt-1 text-fg-muted">
                Overcast cannot run this pipe&apos;s configuration, so nothing flows through it
                whatever its state says. {wiring.Reason}
              </p>
            </div>
          )}

          <section className="flex flex-col gap-3">
            <h2 className="font-mono text-sm font-medium text-fg">Wiring</h2>
            <div className="flex flex-wrap items-stretch gap-2">
              <Leg
                icon={GitBranch}
                label="Source"
                type={wiring?.SourceType ?? "Unknown"}
                arn={pipe?.Source ?? wiring?.Source ?? "—"}
              />
              <div className="flex items-center text-fg-subtle">
                <ArrowRight className="h-4 w-4" />
              </div>
              {wiring?.Enrichment ? (
                <>
                  <Leg
                    icon={Sparkles}
                    label="Enrichment"
                    type={wiring.EnrichmentType ?? "Unknown"}
                    arn={wiring.Enrichment}
                  />
                  <div className="flex items-center text-fg-subtle">
                    <ArrowRight className="h-4 w-4" />
                  </div>
                </>
              ) : null}
              <Leg
                icon={Target}
                label="Target"
                type={wiring?.TargetType ?? "Unknown"}
                arn={pipe?.Target ?? wiring?.Target ?? "—"}
              />
            </div>
          </section>

          <section className="flex flex-col gap-3">
            <h2 className="font-mono text-sm font-medium text-fg">Recent executions</h2>
            <ResourceTable
              variant="embedded"
              query={{ data: deliveries, isLoading: false }}
              noun="executions"
              emptyIcon={GitBranch}
              emptyTitle="No executions yet"
              emptyDescription="Executions are kept in memory only, so this is empty after a restart."
              columnToggle={false}
              // The feed arrives newest-first; the default sort says so
              // explicitly rather than relying on the server's order.
              defaultSort={{ id: "time", desc: true }}
              // `ResourceTable`'s `rowKey` sees the item only, never its index,
              // so the key is composed from the fields that vary. `Time` is
              // RFC3339 with nanoseconds, which makes it unique on its own.
              rowKey={(d) => `${d.Time}-${d.TargetArn}-${d.Outcome}-${d.Attempts}`}
              columns={[
                {
                  id: "time",
                  header: "Time",
                  cellClassName: "text-fg-muted",
                  sortValue: (d) => new Date(d.Time),
                  cell: (d) => new Date(d.Time).toLocaleTimeString(),
                },
                { header: "Records", sortValue: (d) => d.Records, cell: (d) => d.Records },
                {
                  header: "Target",
                  cell: (d) => <Badge variant="accent">{d.TargetType}</Badge>,
                },
                {
                  header: "Outcome",
                  sortValue: (d) => d.Outcome,
                  cell: (d) => {
                    const outcome = OUTCOME_LABELS[d.Outcome]
                    return (
                      <Badge variant={outcome?.variant ?? "default"}>
                        {outcome?.label ?? d.Outcome}
                      </Badge>
                    )
                  },
                },
                { header: "Detail", prose: true, cell: (d) => d.Error || "—" },
              ]}
            />
          </section>
        </>
      )}
    </div>
  )
}
