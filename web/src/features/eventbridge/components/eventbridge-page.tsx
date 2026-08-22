import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { CalendarClock } from "lucide-react"
import {
  ebBusesQueryOptions,
  ebRulesQueryOptions,
  ebKeys,
  deleteBusMutationOptions,
  deleteRuleMutationOptions,
  createBusMutationOptions,
  createRuleMutationOptions,
  type EventBridgeTab,
} from "@/features/eventbridge/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Trash2 } from "lucide-react"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { CreateAction, RefreshAction, ResourceListFilter } from "@/components/ui/resource-list-page"
import { ResourceListSection } from "@/components/ui/resource-list-section"
import { ResourceTable } from "@/components/ui/resource-table"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { CreateResourceDialog } from "@/components/create-resource-dialog"

interface FilterProps {
  /** Current filter text for whichever tab is active — owned by the route's `q` search param, see `useFilterSearchParam`. */
  filter: string
  onFilterChange: (value: string) => void
}

interface EventBridgePageProps extends FilterProps {
  /** Selected tab — owned by the route's `tab` search param, so the tab survives a reload/share. */
  tab: EventBridgeTab
  onTabChange: (tab: EventBridgeTab) => void
}

export function EventBridgePage({
  tab,
  onTabChange,
  filter,
  onFilterChange,
}: EventBridgePageProps) {
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="EventBridge"
        description="Event buses, rules, and targets"
        actions={
          <ServiceDocsButton
            service="eventbridge"
            label="EventBridge"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
        }
      />
      <InertBanner serviceName="EventBridge" />
      <Tabs selectedKey={tab} onSelectionChange={(key) => onTabChange(key as EventBridgeTab)}>
        <TabList>
          <Tab id="buses">Event Buses</Tab>
          <Tab id="rules">Rules</Tab>
        </TabList>
        <TabPanel id="buses">
          <BusesTab filter={filter} onFilterChange={onFilterChange} />
        </TabPanel>
        <TabPanel id="rules">
          <RulesTab filter={filter} onFilterChange={onFilterChange} />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Buses Tab ─────────────────────────────────────────────────────────────

function BusesTab({ filter, onFilterChange }: FilterProps) {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const {
    data: buses = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ebBusesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteBusMutationOptions(),
    invalidateKeys: [ebKeys.buses()],
    successTitle: "Event bus deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? buses.filter((b) => (b.Name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : buses,
    [buses, filter],
  )

  return (
    <ResourceListSection
      className="pt-4"
      actions={
        <>
          <ResourceListFilter
            value={filter}
            onChange={onFilterChange}
            placeholder="Filter buses…"
            className="flex-1"
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create Bus</CreateAction>
        </>
      }
    >
      <ResourceTable
        variant="embedded"
        query={{ data: filtered, isLoading, error }}
        noun="buses"
        emptyIcon={CalendarClock}
        emptyTitle="No event buses"
        emptyDescription="Create an event bus to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create Bus</CreateAction>}
        isFiltered={!!filter}
        onClearFilter={() => onFilterChange("")}
        rowKey={(bus) => bus.Name ?? ""}
        onRowClick={(bus) =>
          navigate({ to: "/eventbridge/$busName", params: { busName: bus.Name ?? "" } })
        }
        columns={[
          { header: "Name", cell: (bus) => bus.Name },
          { header: "ARN", cellClassName: "text-fg-muted", cell: (bus) => bus.Arn },
        ]}
        rowActions={(bus) => (
          <Button
            size="sm"
            variant="ghost"
            className="text-danger hover:text-danger"
            onClick={() => setDeleteTarget(bus.Name)}
            disabled={bus.Name === "default"}
          >
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        )}
      />

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create Event Bus"
        label="Bus Name"
        placeholder="my-event-bus"
        mutationOptions={createBusMutationOptions}
        invalidateKeys={[ebKeys.buses()]}
        successTitle="Event bus created"
      />
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(undefined)}
        title="Delete Event Bus"
        description={
          <>
            Delete bus <span className="font-mono font-semibold">{deleteTarget}</span>? This cannot
            be undone.
          </>
        }
        confirmLabel="Delete"
        variant="danger"
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </ResourceListSection>
  )
}

// ─── Rules Tab ─────────────────────────────────────────────────────────────

function RulesTab({ filter, onFilterChange }: FilterProps) {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const {
    data: rules = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(ebRulesQueryOptions())

  const deleteMut = useResourceMutation({
    options: deleteRuleMutationOptions(),
    invalidateKeys: [ebKeys.rules()],
    successTitle: "Rule deleted",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const filtered = useMemo(
    () =>
      filter
        ? rules.filter((r) => (r.Name ?? "").toLowerCase().includes(filter.toLowerCase()))
        : rules,
    [rules, filter],
  )

  return (
    <ResourceListSection
      className="pt-4"
      actions={
        <>
          <ResourceListFilter
            value={filter}
            onChange={onFilterChange}
            placeholder="Filter rules…"
            className="flex-1"
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
          <CreateAction onClick={() => setShowCreate(true)}>Create Rule</CreateAction>
        </>
      }
    >
      <ResourceTable
        variant="embedded"
        query={{ data: filtered, isLoading, error }}
        noun="rules"
        emptyIcon={CalendarClock}
        emptyTitle="No rules"
        emptyDescription="Create an event rule to get started."
        emptyAction={<CreateAction onClick={() => setShowCreate(true)}>Create Rule</CreateAction>}
        isFiltered={!!filter}
        onClearFilter={() => onFilterChange("")}
        rowKey={(rule) => rule.Name ?? ""}
        columns={[
          { header: "Name", cell: (rule) => rule.Name },
          {
            header: "Event Bus",
            cellClassName: "text-fg-muted",
            cell: (rule) => rule.EventBusName,
          },
          {
            header: "State",
            cell: (rule) => (
              <Badge variant={rule.State === "ENABLED" ? "success" : "default"}>{rule.State}</Badge>
            ),
          },
        ]}
        onDelete={{
          target: rules.find((r) => r.Name === deleteTarget),
          onRequest: (rule) => setDeleteTarget(rule.Name),
          onOpenChange: (open) => !open && setDeleteTarget(undefined),
          mutation: deleteMut,
          getId: (rule) => rule.Name ?? "",
          label: (rule) => rule.Name ?? "",
          noun: "rule",
          title: "Delete Rule",
          description: (rule) => (
            <>
              Delete rule <span className="font-mono font-semibold">{rule.Name}</span>? This cannot
              be undone.
            </>
          ),
          actionLabel: (rule) => `Delete ${rule.Name}`,
        }}
      />

      <CreateResourceDialog
        open={showCreate}
        onOpenChange={setShowCreate}
        title="Create Rule"
        label="Rule Name"
        placeholder="my-rule"
        mutationOptions={createRuleMutationOptions}
        invalidateKeys={[ebKeys.rules()]}
        successTitle="Rule created"
      />
    </ResourceListSection>
  )
}
