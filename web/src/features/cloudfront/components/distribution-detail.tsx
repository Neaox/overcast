import { useState } from "react"
import { useParams } from "@tanstack/react-router"
import { useQuery } from "@tanstack/react-query"
import { RefreshCw, RotateCcw } from "lucide-react"
import {
  cloudfrontDistributionQueryOptions,
  cloudfrontInvalidationsQueryOptions,
  cloudfrontKeys,
  createInvalidationMutationOptions,
} from "@/features/cloudfront/data"
import { MonitoringSubscriptionPanel } from "@/features/cloudfront/components/monitoring-subscription-panel"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { ResourceTable } from "@/components/ui/resource-table"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export function DistributionDetail() {
  const { distributionId } = useParams({ strict: false }) as unknown as { distributionId: string }
  const [showInvalidate, setShowInvalidate] = useState(false)
  const [selectedTab, setSelectedTab] = useState("config")

  const { data, isLoading, isFetching, refetch } = useQuery(
    cloudfrontDistributionQueryOptions(distributionId),
  )

  const {
    data: invalidations = [],
    isLoading: invalidationsLoading,
    error: invalidationsError,
  } = useQuery(cloudfrontInvalidationsQueryOptions(distributionId))

  const invalidateMut = useResourceMutation({
    options: createInvalidationMutationOptions(distributionId),
    invalidateKeys: [cloudfrontKeys.invalidationList(distributionId)],
    successTitle: "Invalidation created",
    successDescription: () => distributionId,
    onSuccess: () => setShowInvalidate(false),
  })

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!data) return null

  const { distribution: dist } = data

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={dist.id}
        description={dist.domainName}
        actions={
          <>
            <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
              <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button size="sm" variant="outline" onClick={() => setShowInvalidate(true)}>
              <RotateCcw className="mr-1.5 h-3.5 w-3.5" />
              Create Invalidation
            </Button>
          </>
        }
      />

      <ApplicationOwnershipBanner candidates={[dist.arn, dist.id]} />

      {/* ── Status badges ── */}
      <div className="flex flex-wrap gap-2">
        <Badge variant={dist.status === "Deployed" ? "success" : "warning"}>{dist.status}</Badge>
        <Badge variant={dist.enabled ? "accent" : "default"}>
          {dist.enabled ? "Enabled" : "Disabled"}
        </Badge>
        {dist.priceClass && <Badge variant="default">{dist.priceClass}</Badge>}
        {dist.httpVersion && <Badge variant="default">HTTP/{dist.httpVersion}</Badge>}
      </div>

      <Tabs selectedKey={selectedTab} onSelectionChange={setSelectedTab}>
        <TabList>
          <Tab id="config">Configuration</Tab>
          <Tab id="origins">Origins ({dist.origins.length})</Tab>
          <Tab id="invalidations">Invalidations ({invalidations.length})</Tab>
          <Tab id="monitoring">Monitoring</Tab>
        </TabList>

        {/* ── Config tab ── */}
        <TabPanel id="config">
          {/* Neither table fits because this is not a resource list: it is a fixed
              label/value grid of one distribution's attributes, with no rows to
              sort, hide or act on. See CONTRIBUTING § Tables. */}
          <DefinitionList className="rounded-md border border-border p-4">
            <Definition label="Distribution ID" value={dist.id} copyable />
            <Definition label="Domain Name" value={dist.domainName} copyable />
            <Definition
              label="Last Modified"
              value={
                dist.lastModifiedTime ? new Date(dist.lastModifiedTime).toLocaleString() : undefined
              }
            />
            <Definition label="Default Root Object" value={dist.defaultRootObject} />
            <Definition
              label="Aliases"
              value={
                dist.aliases.length > 0 ? (
                  <div className="flex flex-wrap gap-1">
                    {dist.aliases.map((a) => (
                      <Badge key={a} variant="default">
                        {a}
                      </Badge>
                    ))}
                  </div>
                ) : undefined
              }
            />
            <Definition label="Comment" value={dist.comment} variant="prose" />
            <Definition label="ARN" value={dist.arn} copyable full />
          </DefinitionList>
        </TabPanel>

        {/* ── Origins tab ── */}
        <TabPanel id="origins">
          <ResourceTable
            variant="embedded"
            query={{ data: dist.origins, isLoading: false }}
            noun="origins"
            emptyTitle="No origins"
            rowKey={(origin) => origin.id}
            columns={[
              {
                id: "origin-id",
                header: "Origin ID",
                sortValue: (origin) => origin.id,
                cell: (origin) => origin.id,
              },
              {
                id: "domain-name",
                header: "Domain Name",
                sortValue: (origin) => origin.domainName,
                cell: (origin) => origin.domainName,
              },
              {
                header: "Type",
                cell: (origin) => (
                  <Badge variant={origin.s3OriginConfig ? "info" : "default"}>
                    {origin.s3OriginConfig ? "S3" : "Custom"}
                  </Badge>
                ),
              },
              {
                header: "Path",
                cellClassName: "text-fg-muted",
                cell: (origin) => origin.originPath || "/",
              },
            ]}
          />

          {/* Origin groups give a behavior a primary and a failover origin, so
              the origin list alone does not explain where traffic goes. Only
              rendered when the distribution actually defines one. */}
          {dist.originGroups.length > 0 && (
            <div className="mt-6">
              <h3 className="mb-2 text-sm font-medium">Origin Groups</h3>
              <ResourceTable
                variant="embedded"
                query={{ data: dist.originGroups, isLoading: false }}
                noun="origin groups"
                emptyTitle="No origin groups"
                rowKey={(group) => group.id}
                columns={[
                  {
                    id: "group-id",
                    header: "Group ID",
                    sortValue: (group) => group.id,
                    cell: (group) => group.id,
                  },
                  { header: "Primary", cell: (group) => group.members[0] ?? "—" },
                  {
                    header: "Failover",
                    cellClassName: "text-fg-muted",
                    cell: (group) => group.members.slice(1).join(", ") || "—",
                  },
                  {
                    header: "Failover On",
                    cell: (group) =>
                      group.failoverStatusCodes.length > 0 ? (
                        <div className="flex flex-wrap gap-1">
                          {group.failoverStatusCodes.map((code) => (
                            <Badge key={code} variant="default">
                              {code}
                            </Badge>
                          ))}
                        </div>
                      ) : (
                        <span className="text-fg-muted">—</span>
                      ),
                  },
                ]}
              />
            </div>
          )}
        </TabPanel>

        {/* ── Invalidations tab ── */}
        <TabPanel id="invalidations">
          <ResourceTable
            variant="embedded"
            query={{
              data: invalidations,
              isLoading: invalidationsLoading,
              error: invalidationsError,
            }}
            noun="invalidations"
            emptyTitle="No invalidations yet"
            emptyAction={
              <Button size="sm" variant="outline" onClick={() => setShowInvalidate(true)}>
                Create Invalidation
              </Button>
            }
            errorTitle="Failed to load invalidations"
            rowKey={(inv) => inv.id}
            columns={[
              {
                id: "id",
                header: "ID",
                sortValue: (inv) => inv.id,
                cell: (inv) => inv.id,
              },
              {
                header: "Status",
                cell: (inv) => (
                  <Badge variant={inv.status === "Completed" ? "success" : "warning"}>
                    {inv.status}
                  </Badge>
                ),
              },
              {
                id: "created",
                header: "Created",
                cellClassName: "text-fg-muted",
                sortValue: (inv) => (inv.createTime ? new Date(inv.createTime) : undefined),
                cell: (inv) => (inv.createTime ? new Date(inv.createTime).toLocaleString() : "—"),
              },
              {
                header: "Paths",
                cell: (inv) => (
                  <div className="flex flex-wrap gap-1">
                    {inv.paths.map((p) => (
                      <Badge key={p} variant="default" className="font-mono text-xs">
                        {p}
                      </Badge>
                    ))}
                  </div>
                ),
              },
            ]}
          />
        </TabPanel>

        {/* ── Monitoring tab ── */}
        <TabPanel id="monitoring">
          <MonitoringSubscriptionPanel distributionId={distributionId} />
        </TabPanel>
      </Tabs>

      {/* ── Create invalidation dialog ── */}
      <CreateInvalidationDialog
        open={showInvalidate}
        onClose={() => setShowInvalidate(false)}
        isPending={invalidateMut.isPending}
        onSubmit={(paths) => invalidateMut.mutate(paths)}
      />
    </div>
  )
}

// ─── CreateInvalidationDialog ─────────────────────────────────────────────────

function CreateInvalidationDialog({
  open,
  onClose,
  onSubmit,
  isPending,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (paths: string[]) => void
  isPending: boolean
}) {
  const [pathInput, setPathInput] = useState("/*")

  function handleClose() {
    onClose()
    setPathInput("/*")
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const paths = pathInput
      .split("\n")
      .map((p) => p.trim())
      .filter(Boolean)
    if (paths.length > 0) onSubmit(paths)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Invalidation</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit}>
          <DialogBody className="flex flex-col gap-4">
            <p className="text-sm text-fg-muted">
              Enter one path per line. Use <code className="text-xs">/*</code> to invalidate all
              objects.
            </p>
            <Input
              value={pathInput}
              onChange={(e) => setPathInput(e.target.value)}
              placeholder="/*"
            />
          </DialogBody>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={handleClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending && <Spinner className="mr-2" />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
