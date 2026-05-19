import { useState } from "react"
import { useParams, useNavigate } from "@tanstack/react-router"
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
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { PageHeader, Breadcrumb, Spinner } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export function DistributionDetail() {
  const { distributionId } = useParams({ strict: false }) as unknown as { distributionId: string }
  const navigate = useNavigate()
  const [showInvalidate, setShowInvalidate] = useState(false)
  const [selectedTab, setSelectedTab] = useState("config")

  const { data, isLoading, isFetching, refetch } = useQuery(
    cloudfrontDistributionQueryOptions(distributionId),
  )

  const { data: invalidations = [], isLoading: invalidationsLoading } = useQuery(
    cloudfrontInvalidationsQueryOptions(distributionId),
  )

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
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "CloudFront", onClick: () => void navigate({ to: "/cloudfront" }) },
              { label: dist.id },
            ]}
          />
        }
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
          <div className="rounded-md border border-border">
            <Table>
              <TableBody>
                <TableRow>
                  <TableCell className="w-48 font-medium">Distribution ID</TableCell>
                  <TableCell className="font-mono text-sm">{dist.id}</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="font-medium">ARN</TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">{dist.arn}</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="font-medium">Domain Name</TableCell>
                  <TableCell className="font-mono text-sm">{dist.domainName}</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="font-medium">Comment</TableCell>
                  <TableCell>{dist.comment || "—"}</TableCell>
                </TableRow>
                <TableRow>
                  <TableCell className="font-medium">Default Root Object</TableCell>
                  <TableCell className="font-mono text-sm">
                    {dist.defaultRootObject || "—"}
                  </TableCell>
                </TableRow>
                {dist.aliases.length > 0 && (
                  <TableRow>
                    <TableCell className="font-medium">Aliases</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {dist.aliases.map((a) => (
                          <Badge key={a} variant="default">
                            {a}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                  </TableRow>
                )}
                <TableRow>
                  <TableCell className="font-medium">Last Modified</TableCell>
                  <TableCell className="text-fg-muted">
                    {dist.lastModifiedTime ? new Date(dist.lastModifiedTime).toLocaleString() : "—"}
                  </TableCell>
                </TableRow>
              </TableBody>
            </Table>
          </div>
        </TabPanel>

        {/* ── Origins tab ── */}
        <TabPanel id="origins">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Origin ID</TableHead>
                <TableHead>Domain Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Path</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {dist.origins.map((origin) => (
                <TableRow key={origin.id}>
                  <TableCell className="font-mono text-xs">{origin.id}</TableCell>
                  <TableCell className="font-mono text-xs">{origin.domainName}</TableCell>
                  <TableCell>
                    <Badge variant={origin.s3OriginConfig ? "info" : "default"}>
                      {origin.s3OriginConfig ? "S3" : "Custom"}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-fg-muted">
                    {origin.originPath || "/"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TabPanel>

        {/* ── Invalidations tab ── */}
        <TabPanel id="invalidations">
          {invalidationsLoading ? (
            <div className="flex justify-center py-8">
              <Spinner className="h-5 w-5" />
            </div>
          ) : invalidations.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8 text-fg-muted">
              <p>No invalidations yet</p>
              <Button size="sm" variant="outline" onClick={() => setShowInvalidate(true)}>
                Create Invalidation
              </Button>
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Paths</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {invalidations.map((inv) => (
                  <TableRow key={inv.id}>
                    <TableCell className="font-mono text-xs">{inv.id}</TableCell>
                    <TableCell>
                      <Badge variant={inv.status === "Completed" ? "success" : "warning"}>
                        {inv.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {inv.createTime ? new Date(inv.createTime).toLocaleString() : "—"}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {inv.paths.map((p) => (
                          <Badge key={p} variant="default" className="font-mono text-xs">
                            {p}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
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
