import { useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { AlertTriangle, RefreshCw, ListTree } from "lucide-react"
import {
  sfnStateMachinesQueryOptions,
  sfnExecutionsQueryOptions,
  sfnExecutionQueryOptions,
  sfnExecutionHistoryQueryOptions,
} from "@/features/stepfunctions/data"
import { Button } from "@/components/ui/button"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import {
  PageHeader,
  Spinner,
  EmptyState,
  CodeBlock,
  SectionLabel,
} from "@/components/ui/primitives"
import { ArnText } from "@/components/ui/arn-link"
import { cn } from "@/lib/utils"
import {
  executionStatusVariant,
  historyEventVariant,
  historyEventStateName,
  historyEventFailure,
  formatTimestamp,
  prettyJSON,
} from "@/features/stepfunctions/format"

interface Props {
  /** State machine name, from the route. */
  name: string
  /** Execution name, from the route. */
  execution: string
}

/**
 * Execution detail: status, input and output, and the state-by-state history
 * the interpreter recorded. When an execution failed — including the loud
 * "Overcast does not support …" failures raised for ASL it cannot interpret —
 * the reason is shown at the top rather than leaving a bare FAILED.
 */
export function ExecutionDetail({ name, execution }: Props) {
  const { data: machines = [], isLoading: listLoading } = useQuery(sfnStateMachinesQueryOptions())
  const stateMachineArn = useMemo(
    () => machines.find((m) => m.name === name)?.stateMachineArn ?? "",
    [machines, name],
  )

  const { data: executions = [] } = useQuery(sfnExecutionsQueryOptions(stateMachineArn))
  const executionArn = useMemo(
    () => executions.find((e) => e.name === execution)?.executionArn ?? "",
    [executions, execution],
  )

  const { data: detail, isFetching, refetch } = useQuery(sfnExecutionQueryOptions(executionArn))
  const { data: events = [], isLoading: historyLoading } = useQuery(
    sfnExecutionHistoryQueryOptions(executionArn),
  )

  if (listLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!executionArn) {
    return (
      <EmptyState
        icon={<ListTree className="h-6 w-6" />}
        title="Execution not found"
        description={`No execution named "${execution}" on state machine "${name}".`}
        action={
          <Button size="sm" asChild>
            <Link to="/stepfunctions/$name" params={{ name }}>
              Back to {name}
            </Link>
          </Button>
        }
      />
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={execution}
        meta={<ArnText arn={executionArn} />}
        description={
          <Link className="text-accent hover:underline" to="/stepfunctions/$name" params={{ name }}>
            {name}
          </Link>
        }
        actions={
          <>
            <Badge variant={executionStatusVariant(detail?.status)}>{detail?.status ?? "—"}</Badge>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
          </>
        }
      />

      {detail?.error && (
        <div className="flex items-start gap-3 rounded-lg border border-danger/30 bg-danger-muted px-4 py-3 text-sm text-danger">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div className="flex min-w-0 flex-col gap-1">
            <span className="font-medium">{detail.error}</span>
            {detail.cause && <span className="break-words text-fg-muted">{detail.cause}</span>}
          </div>
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <div className="flex min-w-0 flex-col gap-2">
          <SectionLabel>Input</SectionLabel>
          <CodeBlock className="max-h-64">{prettyJSON(detail?.input) || "—"}</CodeBlock>
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          <SectionLabel>Output</SectionLabel>
          <CodeBlock className="max-h-64">{prettyJSON(detail?.output) || "—"}</CodeBlock>
        </div>
      </div>

      <div className="flex flex-col gap-1 text-xs text-fg-muted">
        <span>Started {formatTimestamp(detail?.startDate)}</span>
        <span>Stopped {formatTimestamp(detail?.stopDate)}</span>
      </div>

      <div className="flex flex-col gap-2">
        <SectionLabel>State history</SectionLabel>
        <ResourceTable
          variant="embedded"
          query={{ data: events, isLoading: historyLoading }}
          noun="events"
          emptyTitle="No history"
          emptyDescription="This execution recorded no events."
          rowKey={(event) => Number(event.id ?? 0)}
          // The interpreter records events in the order they happened, and the
          // ids count up with them; #2 after #10 would be a lie about the run.
          columns={[
            {
              id: "id",
              header: "#",
              headerClassName: "w-12",
              cellClassName: "text-fg-subtle",
              cell: (event) => Number(event.id ?? 0),
            },
            {
              header: "Event",
              cell: (event) => (
                <Badge variant={historyEventVariant(event.type)}>{event.type}</Badge>
              ),
            },
            {
              header: "State",
              cellClassName: "font-mono text-xs",
              cell: (event) => historyEventStateName(event) || "—",
            },
            {
              header: "Time",
              cellClassName: "text-fg-muted",
              cell: (event) => formatTimestamp(event.timestamp),
            },
          ]}
          // Several events open at once is the point: it is how two failures get
          // read side by side rather than one at a time in a pane below.
          expandedContent={(event) => {
            const failure = historyEventFailure(event as unknown as Record<string, unknown>)
            return (
              <>
                {failure.error && (
                  <p className="mb-2 text-xs text-danger">
                    <span className="font-medium">{failure.error}</span>
                    {failure.cause ? ` — ${failure.cause}` : ""}
                  </p>
                )}
                <CodeBlock className="max-h-64">{JSON.stringify(event, null, 2)}</CodeBlock>
              </>
            )
          }}
        />
      </div>
    </div>
  )
}
