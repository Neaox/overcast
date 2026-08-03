import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Play, RefreshCw, ListTree } from "lucide-react"
import {
  sfnStateMachineQueryOptions,
  sfnStateMachinesQueryOptions,
  sfnExecutionsQueryOptions,
  sfnKeys,
  startExecutionMutationOptions,
} from "@/features/stepfunctions/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
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
  formatTimestamp,
  prettyJSON,
} from "@/features/stepfunctions/format"

interface Props {
  /** State machine name, taken from the route. */
  name: string
}

/**
 * State machine detail: the stored ASL definition plus every execution that has
 * run against it. Executions are real — the interpreter runs them — so this is
 * the page that shows whether a workflow actually did what it was meant to.
 */
export function StateMachineDetail({ name }: Props) {
  const [showStart, setShowStart] = useState(false)
  const [input, setInput] = useState("{}")

  // Executions and the definition are both addressed by ARN; the route carries
  // the name, so resolve it through the list first.
  const { data: machines = [], isLoading: listLoading } = useQuery(sfnStateMachinesQueryOptions())
  const arn = useMemo(
    () => machines.find((m) => m.name === name)?.stateMachineArn ?? "",
    [machines, name],
  )

  const { data: machine } = useQuery(sfnStateMachineQueryOptions(arn))
  const {
    data: executions = [],
    isLoading: execsLoading,
    isFetching,
    refetch,
  } = useQuery(sfnExecutionsQueryOptions(arn))

  const startMut = useResourceMutation({
    options: startExecutionMutationOptions(),
    invalidateKeys: [sfnKeys.executions(arn)],
    successTitle: "Execution started",
    onSuccess: () => setShowStart(false),
  })

  const definition = prettyJSON(machine?.definition)

  if (listLoading) {
    return (
      <div className="flex justify-center py-16">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!arn) {
    return (
      <EmptyState
        icon={<ListTree className="h-6 w-6" />}
        title="State machine not found"
        description={`No state machine named "${name}".`}
        action={
          <Button size="sm" asChild>
            <Link to="/stepfunctions">Back to state machines</Link>
          </Button>
        }
      />
    )
  }

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={name}
        meta={<ArnText arn={arn} />}
        description={`${executions.length} execution${executions.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowStart(true)}>
              <Play className="mr-1.5 h-3.5 w-3.5" />
              Start Execution
            </Button>
          </>
        }
      />

      <div className="flex flex-col gap-2">
        <SectionLabel>Definition</SectionLabel>
        <CodeBlock className="max-h-64">{definition || "—"}</CodeBlock>
      </div>

      <div className="flex flex-col gap-2">
        <SectionLabel>Executions</SectionLabel>
        {execsLoading ? (
          <div className="flex justify-center py-16">
            <Spinner className="h-6 w-6" />
          </div>
        ) : executions.length === 0 ? (
          <EmptyState
            icon={<Play className="h-6 w-6" />}
            title="No executions"
            description="Start an execution to run this state machine."
            action={
              <Button size="sm" onClick={() => setShowStart(true)}>
                <Play className="mr-1.5 h-3.5 w-3.5" /> Start Execution
              </Button>
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Started</TableHead>
                <TableHead>Stopped</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {executions.map((execution) => (
                <TableRow key={execution.executionArn}>
                  <TableCell>
                    <Link
                      className="font-medium text-accent hover:underline"
                      to="/stepfunctions/execution/$name/$execution"
                      params={{ name, execution: execution.name ?? "" }}
                    >
                      {execution.name}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Badge variant={executionStatusVariant(execution.status)}>
                      {execution.status}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    {formatTimestamp(execution.startDate)}
                  </TableCell>
                  <TableCell className="text-fg-muted">
                    {formatTimestamp(execution.stopDate)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <Dialog open={showStart} onOpenChange={setShowStart}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Start Execution</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <SectionLabel>Input (JSON)</SectionLabel>
            <Textarea
              rows={8}
              className="font-mono text-xs"
              value={input}
              onChange={(e) => setInput(e.target.value)}
            />
            <p className="text-xs text-fg-subtle">
              The execution starts in the background — refresh to watch it move from RUNNING to its
              final state.
            </p>
          </div>
          <DialogFooter>
            <Button size="sm" variant="ghost" onClick={() => setShowStart(false)}>
              Cancel
            </Button>
            <Button
              size="sm"
              disabled={startMut.isPending}
              onClick={() => startMut.mutate({ stateMachineArn: arn, input })}
            >
              Start
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
