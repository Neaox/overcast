import { Fragment, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { AlertTriangle, ChevronDown, ChevronRight, RefreshCw, ListTree } from "lucide-react"
import {
  sfnStateMachinesQueryOptions,
  sfnExecutionsQueryOptions,
  sfnExecutionQueryOptions,
  sfnExecutionHistoryQueryOptions,
} from "@/features/stepfunctions/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
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
  const [expanded, setExpanded] = useState<Set<number>>(new Set())

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

  const toggle = (id: number) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

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
        {historyLoading ? (
          <div className="flex justify-center py-16">
            <Spinner className="h-6 w-6" />
          </div>
        ) : events.length === 0 ? (
          <EmptyState title="No history" description="This execution recorded no events." />
        ) : (
          // ResourceTable didn't fit because the state history is an expandable
          // list, not a flat one: clicking a row inserts a second full-width
          // `<tr>` under it holding the failure line and the event's raw JSON.
          // `ResourceTable` renders exactly one `<TableRow>` per row of its
          // model and registers none of v9's expanding features
          // (`rowExpandingFeature` is on the deliberately-skipped list in
          // `resource-table.tsx`), so there is no seam to render the detail row
          // through — and folding the JSON into the last cell instead would
          // trade a full-width code panel for a column-width one. Revisit if
          // #1327 ever adopts row expansion; the rest of this table is an
          // ordinary `ResourceTable` shape.
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-12">#</TableHead>
                  <TableHead>Event</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Time</TableHead>
                  <TableHead className="w-8" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((event) => {
                  const id = Number(event.id ?? 0)
                  const failure = historyEventFailure(event as unknown as Record<string, unknown>)
                  const isOpen = expanded.has(id)
                  return (
                    <Fragment key={id}>
                      <TableRow className="cursor-pointer" onClick={() => toggle(id)}>
                        <TableCell className="text-fg-subtle">{id}</TableCell>
                        <TableCell>
                          <Badge variant={historyEventVariant(event.type)}>{event.type}</Badge>
                        </TableCell>
                        <TableCell className="font-mono text-xs">
                          {historyEventStateName(event) || "—"}
                        </TableCell>
                        <TableCell className="text-fg-muted">
                          {formatTimestamp(event.timestamp)}
                        </TableCell>
                        <TableCell className="text-fg-subtle">
                          {isOpen ? (
                            <ChevronDown className="h-3.5 w-3.5" />
                          ) : (
                            <ChevronRight className="h-3.5 w-3.5" />
                          )}
                        </TableCell>
                      </TableRow>
                      {isOpen && (
                        <TableRow>
                          <TableCell colSpan={5}>
                            {failure.error && (
                              <p className="mb-2 text-xs text-danger">
                                <span className="font-medium">{failure.error}</span>
                                {failure.cause ? ` — ${failure.cause}` : ""}
                              </p>
                            )}
                            <CodeBlock className="max-h-64">
                              {JSON.stringify(event, null, 2)}
                            </CodeBlock>
                          </TableCell>
                        </TableRow>
                      )}
                    </Fragment>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </div>
  )
}
