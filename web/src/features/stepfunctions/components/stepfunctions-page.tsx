import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Shuffle } from "lucide-react"
import {
  sfnStateMachinesQueryOptions,
  sfnKeys,
  deleteStateMachineMutationOptions,
  createStateMachineMutationOptions,
} from "@/features/stepfunctions/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import {
  CreateAction,
  RefreshAction,
  ResourceListFilter,
  ResourceListPage,
} from "@/components/ui/resource-list-page"
import { ResourceTable } from "@/components/ui/resource-table"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { CreateResourceDialog } from "@/components/create-resource-dialog"
import { ArnText } from "@/components/ui/arn-link"

export function StepFunctionsPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: machines = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(sfnStateMachinesQueryOptions())

  const [deleteTarget, setDeleteTarget] = useState<(typeof machines)[number]>()

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
    <ResourceListPage
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
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create State Machine</CreateAction>
        </>
      }
    >
      <ResourceListFilter
        value={filter}
        onChange={setFilter}
        placeholder="Filter state machines…"
      />

      <ResourceTable
        query={{ data: filtered, isLoading, error }}
        noun="state machines"
        emptyIcon={Shuffle}
        emptyTitle="No state machines"
        emptyDescription={
          filter ? "No state machines match the filter." : "Create a state machine to get started."
        }
        emptyAction={
          !filter && (
            <CreateAction onClick={() => setShowCreate(true)}>Create State Machine</CreateAction>
          )
        }
        rowKey={(sm) => sm.stateMachineArn ?? ""}
        columns={[
          {
            header: "Name",
            cell: (sm) => (
              <Link
                className="font-medium text-accent hover:underline"
                to="/stepfunctions/$name"
                params={{ name: sm.name ?? "" }}
              >
                {sm.name}
              </Link>
            ),
          },
          { header: "Type", cell: (sm) => <Badge variant="default">{sm.type}</Badge> },
          {
            header: "ARN",
            cellClassName: "text-fg-muted",
            cell: (sm) => <ArnText arn={sm.stateMachineArn ?? ""} />,
          },
        ]}
        onDelete={{
          target: deleteTarget,
          onRequest: setDeleteTarget,
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (sm) => sm.stateMachineArn ?? "",
          label: (sm) => sm.name ?? "",
          noun: "state machine",
          title: "Delete State Machine",
          description: (sm) => (
            <>
              Delete <span className="font-mono font-semibold">{sm.name}</span>? This cannot be
              undone.
            </>
          ),
        }}
      />

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
    </ResourceListPage>
  )
}
