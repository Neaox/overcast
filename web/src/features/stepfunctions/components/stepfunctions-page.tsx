import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Shuffle, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  sfnStateMachinesQueryOptions,
  sfnKeys,
  deleteStateMachineMutationOptions,
  createStateMachineMutationOptions,
} from "@/features/stepfunctions/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { CreateResourceDialog } from "@/components/create-resource-dialog"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function StepFunctionsPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<{ name: string; arn: string }>()
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: machines = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(sfnStateMachinesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteStateMachineMutationOptions(),
    invalidateKeys: [sfnKeys.stateMachines()],
    successTitle: "State machine deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? machines.filter((m) => (m.name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : machines,
    [machines, filter],
  )

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Step Functions"
        description={`${machines.length} state machine${machines.length !== 1 ? "s" : ""}`}
        actions={
          <>
            <ServiceDocsButton
              service="stepfunctions"
              label="Step Functions"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create State Machine
            </Button>
          </>
        }
      />
      <InertBanner serviceName="Step Functions" />

      <div className="relative">
        <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
        <Input
          placeholder="Filter state machines…"
          className="pl-8"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Shuffle className="h-6 w-6" />}
          title="No state machines"
          description={
            filter
              ? "No state machines match the filter."
              : "Create a state machine to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create State Machine
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((sm) => (
              <TableRow key={sm.stateMachineArn}>
                <TableCell className="font-mono text-sm">{sm.name}</TableCell>
                <TableCell>
                  <Badge variant="default">{sm.type}</Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">
                  <ArnText arn={sm.stateMachineArn ?? ""} />
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() =>
                      setDeleteTarget({ name: sm.name ?? "", arn: sm.stateMachineArn ?? "" })
                    }
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create State Machine"
        label="Name"
        placeholder="my-state-machine"
        mutationOptions={createStateMachineMutationOptions}
        invalidateKeys={[sfnKeys.stateMachines()]}
        successTitle="State machine created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete State Machine"
        description={
          <>
            Delete <span className="font-mono font-semibold">{deleteTarget?.name}</span>? This
            cannot be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget.arn)}
      />
    </div>
  )
}
