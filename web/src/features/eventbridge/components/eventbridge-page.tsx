import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { CalendarClock, Plus, Trash2, RefreshCw, Search } from "lucide-react"
import {
  ebBusesQueryOptions,
  ebRulesQueryOptions,
  ebKeys,
  deleteBusMutationOptions,
  deleteRuleMutationOptions,
  createBusMutationOptions,
  createRuleMutationOptions,
} from "@/features/eventbridge/data"
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
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { CreateResourceDialog } from "@/components/create-resource-dialog"
import { cn } from "@/lib/utils"

export function EventBridgePage() {
  const [tab, setTab] = useState("buses")
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
      <Tabs selectedKey={tab} onSelectionChange={setTab}>
        <TabList>
          <Tab id="buses">Event Buses</Tab>
          <Tab id="rules">Rules</Tab>
        </TabList>
        <TabPanel id="buses">
          <BusesTab />
        </TabPanel>
        <TabPanel id="rules">
          <RulesTab />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Buses Tab ─────────────────────────────────────────────────────────────

function BusesTab() {
  const navigate = useNavigate()
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const { data: buses = [], isLoading, isFetching, refetch } = useQuery(ebBusesQueryOptions())

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
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter buses…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} /> Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Bus
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<CalendarClock className="h-6 w-6" />}
          title="No event buses"
          description={
            filter ? "No buses match the filter." : "Create an event bus to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Bus
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>ARN</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((bus) => (
              <TableRow
                key={bus.Name}
                className="cursor-pointer"
                onClick={() =>
                  navigate({ to: "/eventbridge/$busName", params: { busName: bus.Name ?? "" } })
                }
              >
                <TableCell className="font-mono text-sm">{bus.Name}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{bus.Arn}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(bus.Name)}
                    disabled={bus.Name === "default"}
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
    </div>
  )
}

// ─── Rules Tab ─────────────────────────────────────────────────────────────

function RulesTab() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")
  const { data: rules = [], isLoading, isFetching, refetch } = useQuery(ebRulesQueryOptions())

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
    <div className="flex flex-col gap-3 pt-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter rules…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} /> Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Rule
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<CalendarClock className="h-6 w-6" />}
          title="No rules"
          description={
            filter ? "No rules match the filter." : "Create an event rule to get started."
          }
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create Rule
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Event Bus</TableHead>
              <TableHead>State</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((rule) => (
              <TableRow key={rule.Name}>
                <TableCell className="font-mono text-sm">{rule.Name}</TableCell>
                <TableCell className="text-muted-foreground">{rule.EventBusName}</TableCell>
                <TableCell>
                  <Badge variant={rule.State === "ENABLED" ? "success" : "default"}>
                    {rule.State}
                  </Badge>
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-danger hover:text-danger"
                    onClick={() => setDeleteTarget(rule.Name)}
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
        title="Create Rule"
        label="Rule Name"
        placeholder="my-rule"
        mutationOptions={createRuleMutationOptions}
        invalidateKeys={[ebKeys.rules()]}
        successTitle="Rule created"
      />
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
