import { useState, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { CalendarClock, Trash2, RefreshCw, Search } from "lucide-react"
import { ebRulesQueryOptions, ebKeys, deleteRuleMutationOptions } from "@/features/eventbridge/data"
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
import { Badge } from "@/components/ui/badge"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Card, CardContent } from "@/components/ui/card"
import { PageHeader, Breadcrumb, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { cn } from "@/lib/utils"

interface Props {
  busName: string
}

export function EventBusDetail({ busName }: Props) {
  const navigate = useNavigate()
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [filter, setFilter] = useState("")

  const {
    data: allRules = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ebRulesQueryOptions(busName))

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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "EventBridge", onClick: () => navigate({ to: "/eventbridge" }) },
              { label: busName },
            ]}
          />
        }
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
      <Card>
        <CardContent className="grid grid-cols-2 gap-x-8 gap-y-3 p-4 text-sm">
          <DetailRow label="Bus name" value={busName} mono />
          <DetailRow label="Rules" value={String(rules.length)} />
        </CardContent>
      </Card>

      {/* Rules section */}
      <section className="flex flex-col gap-3">
        <h2 className="text-sm font-medium text-fg">Rules</h2>
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
                <TableHead>Description</TableHead>
                <TableHead className="w-16" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {filtered.map((rule) => (
                <TableRow key={rule.Name}>
                  <TableCell className="font-mono text-sm">{rule.Name}</TableCell>
                  <TableCell>
                    <Badge variant={rule.State === "ENABLED" ? "default" : "outline"}>
                      {rule.State}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-sm text-fg-muted">{rule.Description || "—"}</TableCell>
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
              ))}
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

function DetailRow({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className={cn(mono && "font-mono text-xs break-all")}>{value}</span>
    </div>
  )
}
