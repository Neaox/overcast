import { useState } from "react"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import {
  ec2VpcDetailQueryOptions,
  ec2SubnetsQueryOptions,
  ec2SecurityGroupsQueryOptions,
  ec2RouteTablesQueryOptions,
  ec2InternetGatewaysQueryOptions,
  ec2VpcPeeringConnectionsQueryOptions,
  ec2VpcsQueryOptions,
  ec2VpcEndpointsQueryOptions,
  ec2Keys,
  createVpcPeeringConnectionMutationOptions,
  acceptVpcPeeringConnectionMutationOptions,
  deleteVpcPeeringConnectionMutationOptions,
  createInternetGatewayMutationOptions,
  deleteInternetGatewayMutationOptions,
  attachInternetGatewayMutationOptions,
  detachInternetGatewayMutationOptions,
} from "@/features/ec2/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ResourceTable } from "@/components/ui/resource-table"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { Plus, Trash2, Check, Unlink } from "lucide-react"
import { Combobox } from "@/components/ui/combobox"

export function VpcDetail({ vpcId }: { vpcId: string }) {
  const [activeTab, setActiveTab] = useState("overview")

  const { data: vpc, isLoading } = useQuery(ec2VpcDetailQueryOptions(vpcId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!vpc) return null

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={vpc.vpcId}
        description={
          <span className="flex items-center gap-2">
            <code className="text-sm text-fg-muted">{vpc.cidrBlock}</code>
            <Badge variant={vpc.state === "available" ? "success" : "warning"}>{vpc.state}</Badge>
            {vpc.isDefault && <Badge variant="default">default</Badge>}
          </span>
        }
      />

      <ApplicationOwnershipBanner candidates={[vpc.vpcId]} />

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="overview">Overview</Tab>
          <Tab id="subnets">Subnets</Tab>
          <Tab id="route-tables">Route Tables</Tab>
          <Tab id="internet-gateways">Internet Gateways</Tab>
          <Tab id="peering">Peering Connections</Tab>
          <Tab id="endpoints">Endpoints</Tab>
          <Tab id="security-groups">Security Groups</Tab>
          <Tab id="tags">Tags</Tab>
        </TabList>

        <TabPanel id="overview" className="pt-4">
          <OverviewPanel vpc={vpc} />
        </TabPanel>

        <TabPanel id="subnets" className="pt-4">
          <SubnetsPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="route-tables" className="pt-4">
          <RouteTablesPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="internet-gateways" className="pt-4">
          <InternetGatewaysPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="peering" className="pt-4">
          <PeeringPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="endpoints" className="pt-4">
          <EndpointsPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="security-groups" className="pt-4">
          <SecurityGroupsPanel vpcId={vpcId} />
        </TabPanel>

        <TabPanel id="tags" className="pt-4">
          <TagsPanel tags={vpc.tags} />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Overview Panel ───────────────────────────────────────────────────────

const networkStatusVariant: Record<string, "success" | "warning" | "danger" | "default"> = {
  ok: "success",
  shared: "default",
  unbacked: "warning",
  conflict: "danger",
  remapped: "default",
}

const networkStatusTooltip: Record<string, string> = {
  ok: "This VPC owns its Docker network.",
  shared:
    "This VPC shares a Docker network with another VPC that has the same CIDR. Container isolation between sharers is not enforced.",
  unbacked:
    "No Docker network could be assigned (Docker unavailable or deferred). Container launches into this VPC will fail until reconciled.",
  conflict:
    "CIDR overlaps another VPC under the strict strategy. Container launches targeting this VPC will be rejected.",
  remapped:
    "This VPC uses a shadow CIDR for its Docker network. API responses show the original CIDR.",
}

function OverviewPanel({
  vpc,
}: {
  vpc: {
    vpcId: string
    cidrBlock: string
    state: string
    isDefault: boolean
    networkStatus?: string
  }
}) {
  const ns = vpc.networkStatus ?? "ok"
  return (
    <DefinitionList>
      <Definition label="VPC ID" value={vpc.vpcId} />
      <Definition label="CIDR Block" value={vpc.cidrBlock} />
      <Definition
        label="State"
        value={
          <Badge variant={vpc.state === "available" ? "success" : "warning"}>{vpc.state}</Badge>
        }
      />
      <Definition label="Default VPC" value={vpc.isDefault ? "Yes" : "No"} />
      <Definition
        label="Network Status"
        value={
          <span title={networkStatusTooltip[ns] ?? ns}>
            <Badge variant={networkStatusVariant[ns] ?? "default"}>{ns}</Badge>
          </span>
        }
      />
    </DefinitionList>
  )
}

// ─── Subnets Panel ────────────────────────────────────────────────────────

function SubnetsPanel({ vpcId }: { vpcId: string }) {
  const { data: allSubnets = [], isLoading } = useQuery(ec2SubnetsQueryOptions())
  const subnets = allSubnets.filter((s) => s.vpcId === vpcId)

  return (
    <ResourceTable
      variant="embedded"
      columnToggle={false}
      query={{ data: subnets, isLoading }}
      noun="subnets"
      emptyTitle="No subnets"
      emptyDescription="This VPC has no subnets."
      rowKey={(s) => s.subnetId}
      columns={[
        { header: "Subnet ID", sortValue: (s) => s.subnetId, cell: (s) => s.subnetId },
        { header: "CIDR Block", cell: (s) => s.cidrBlock },
        {
          header: "Availability Zone",
          sortValue: (s) => s.availabilityZone,
          cell: (s) => s.availabilityZone,
        },
      ]}
    />
  )
}

// ─── Route Tables Panel ───────────────────────────────────────────────────

function RouteTablesPanel({ vpcId }: { vpcId: string }) {
  const { data: allRTs = [], isLoading } = useQuery(ec2RouteTablesQueryOptions())
  const routeTables = allRTs.filter((rt) => rt.vpcId === vpcId)

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (routeTables.length === 0) {
    return <EmptyState title="No route tables" description="This VPC has no route tables." />
  }

  return (
    <div className="space-y-6">
      {routeTables.map((rt) => (
        <div key={rt.routeTableId} className="space-y-2">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm font-medium">{rt.routeTableId}</span>
            {rt.associations.some((a) => a.main) && <Badge variant="default">Main</Badge>}
          </div>
          <ResourceTable
            variant="embedded"
            columnToggle={false}
            query={{ data: rt.routes, isLoading: false }}
            noun="routes"
            emptyTitle="No routes"
            rowKey={(route) => `${route.destinationCidrBlock}-${route.gatewayId ?? "local"}`}
            columns={[
              {
                header: "Destination",
                sortValue: (route) => route.destinationCidrBlock,
                cell: (route) => route.destinationCidrBlock,
              },
              { header: "Target", cell: (route) => route.gatewayId || "local" },
              { header: "Origin", cellClassName: "text-fg-muted", cell: (route) => route.origin },
            ]}
          />
          {rt.associations.filter((a) => a.subnetId).length > 0 && (
            <p className="text-xs text-fg-muted">
              Associated subnets:{" "}
              {rt.associations
                .filter((a) => a.subnetId)
                .map((a) => a.subnetId)
                .join(", ")}
            </p>
          )}
        </div>
      ))}
    </div>
  )
}

// ─── Internet Gateways Panel ──────────────────────────────────────────────

function InternetGatewaysPanel({ vpcId }: { vpcId: string }) {
  const [deleteTarget, setDeleteTarget] = useState<string>()
  const [detachTarget, setDetachTarget] = useState<string>()

  const { data: allIGWs = [], isLoading } = useQuery(ec2InternetGatewaysQueryOptions())
  const igws = allIGWs.filter((igw) => igw.attachments.some((a) => a.vpcId === vpcId))

  // Unattached IGWs available for attachment
  const unattachedIgws = allIGWs.filter((igw) => igw.attachments.length === 0)

  const [showAttach, setShowAttach] = useState(false)
  const [selectedIgw, setSelectedIgw] = useState("")

  const createMut = useResourceMutation({
    options: createInternetGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.internetGateways()],
    successTitle: "Internet gateway created",
  })

  const attachMut = useResourceMutation({
    options: attachInternetGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.internetGateways()],
    successTitle: "Internet gateway attached",
    onSuccess: () => {
      setShowAttach(false)
      setSelectedIgw("")
    },
  })

  const detachMut = useResourceMutation({
    options: detachInternetGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.internetGateways()],
    successTitle: "Internet gateway detached",
    successVariant: "default",
    onSuccess: () => setDetachTarget(undefined),
  })

  const deleteMut = useResourceMutation({
    options: deleteInternetGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.internetGateways()],
    successTitle: "Internet gateway deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  const handleCreateAndAttach = async () => {
    const igw = await createMut.mutateAsync(undefined)
    attachMut.mutate({ internetGatewayId: igw.internetGatewayId, vpcId })
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button
          size="sm"
          onClick={handleCreateAndAttach}
          disabled={createMut.isPending || attachMut.isPending}
        >
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          {createMut.isPending || attachMut.isPending ? "Creating…" : "Create & Attach"}
        </Button>
        {unattachedIgws.length > 0 && (
          <Button size="sm" variant="secondary" onClick={() => setShowAttach((v) => !v)}>
            Attach Existing
          </Button>
        )}
      </div>

      {showAttach && (
        <div className="flex items-center gap-2 rounded-md border border-border bg-bg-elevated p-3">
          <Combobox<{ value: string; label: string }>
            value={selectedIgw}
            onChange={(v) => setSelectedIgw(v)}
            items={unattachedIgws.map((igw) => ({
              value: igw.internetGatewayId,
              label: igw.internetGatewayId,
            }))}
            filterFn={(item, q) => item.label.toLowerCase().includes(q.toLowerCase())}
            getItemValue={(item) => item.value}
            renderItem={(item: { value: string; label: string }) => (
              <span className="font-mono text-sm">{item.label}</span>
            )}
            placeholder="Select internet gateway…"
            className="flex-1"
          />
          <Button
            size="sm"
            disabled={!selectedIgw || attachMut.isPending}
            onClick={() => attachMut.mutate({ internetGatewayId: selectedIgw, vpcId })}
          >
            Attach
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setShowAttach(false)
              setSelectedIgw("")
            }}
          >
            Cancel
          </Button>
        </div>
      )}

      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: igws, isLoading }}
        noun="internet gateways"
        emptyTitle="No internet gateways"
        emptyDescription="No internet gateway is attached to this VPC."
        emptyAction={
          <Button
            onClick={handleCreateAndAttach}
            disabled={createMut.isPending || attachMut.isPending}
          >
            <Plus className="mr-1.5 h-3.5 w-3.5" />
            Create & Attach
          </Button>
        }
        rowKey={(igw) => igw.internetGatewayId}
        columns={[
          {
            header: "Internet Gateway ID",
            sortValue: (igw) => igw.internetGatewayId,
            cell: (igw) => igw.internetGatewayId,
          },
          {
            header: "State",
            cell: (igw) => {
              const state = igw.attachments.find((a) => a.vpcId === vpcId)?.state
              return (
                <Badge variant={state === "available" ? "success" : "default"}>
                  {state ?? "unknown"}
                </Badge>
              )
            },
          },
        ]}
        rowActions={(igw) => (
          <>
            <Button
              size="icon"
              variant="ghost"
              className="text-fg-muted hover:text-warning"
              title="Detach"
              onClick={() => setDetachTarget(igw.internetGatewayId)}
            >
              <Unlink className="h-3.5 w-3.5" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="text-fg-muted hover:text-danger"
              title="Delete"
              onClick={() => setDeleteTarget(igw.internetGatewayId)}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </>
        )}
      />

      <ConfirmDialog
        open={!!detachTarget}
        onOpenChange={(v) => !v && setDetachTarget(undefined)}
        title="Detach Internet Gateway"
        description={
          <>
            Detach <strong>{detachTarget}</strong> from this VPC?
          </>
        }
        isPending={detachMut.isPending}
        onConfirm={() =>
          detachTarget && detachMut.mutate({ internetGatewayId: detachTarget, vpcId })
        }
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Internet Gateway"
        description={
          <>
            Detach and permanently delete <strong>{deleteTarget}</strong>? This cannot be undone.
          </>
        }
        isPending={deleteMut.isPending || detachMut.isPending}
        onConfirm={async () => {
          if (!deleteTarget) return
          // Must detach before deleting
          const igw = igws.find((g) => g.internetGatewayId === deleteTarget)
          if (igw?.attachments.some((a) => a.vpcId === vpcId)) {
            await detachMut.mutateAsync({ internetGatewayId: deleteTarget, vpcId })
          }
          deleteMut.mutate(deleteTarget)
        }}
      />
    </div>
  )
}

// ─── Peering Connections Panel ────────────────────────────────────────────

function PeeringPanel({ vpcId }: { vpcId: string }) {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  const { data: allPeerings = [], isLoading } = useQuery(ec2VpcPeeringConnectionsQueryOptions())
  const peerings = allPeerings.filter(
    (pcx) =>
      pcx.status.code !== "deleted" &&
      (pcx.requesterVpcInfo.vpcId === vpcId || pcx.accepterVpcInfo.vpcId === vpcId),
  )

  const createMut = useResourceMutation({
    options: createVpcPeeringConnectionMutationOptions(),
    invalidateKeys: [ec2Keys.vpcPeeringConnections()],
    successTitle: "Peering connection created",
    onSuccess: () => setShowCreate(false),
  })

  const acceptMut = useResourceMutation({
    options: acceptVpcPeeringConnectionMutationOptions(),
    invalidateKeys: [ec2Keys.vpcPeeringConnections()],
    successTitle: "Peering connection accepted",
  })

  const deleteMut = useResourceMutation({
    options: deleteVpcPeeringConnectionMutationOptions(),
    invalidateKeys: [ec2Keys.vpcPeeringConnections()],
    successTitle: "Peering connection deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Create Peering Connection
        </Button>
      </div>

      <ResourceTable
        variant="embedded"
        columnToggle={false}
        query={{ data: peerings, isLoading }}
        noun="peering connections"
        emptyTitle="No peering connections"
        emptyDescription="This VPC has no peering connections."
        emptyAction={
          <Button onClick={() => setShowCreate(true)}>
            <Plus className="mr-1.5 h-3.5 w-3.5" />
            Create Peering Connection
          </Button>
        }
        rowKey={(pcx) => pcx.vpcPeeringConnectionId}
        columns={[
          {
            header: "Peering ID",
            sortValue: (pcx) => pcx.vpcPeeringConnectionId,
            cell: (pcx) => pcx.vpcPeeringConnectionId,
          },
          {
            header: "Requester VPC",
            cell: (pcx) => (
              <>
                <VpcLink vpcId={pcx.requesterVpcInfo.vpcId} currentVpcId={vpcId} />
                <span className="ml-1 text-fg-muted">({pcx.requesterVpcInfo.cidrBlock})</span>
              </>
            ),
          },
          {
            header: "Accepter VPC",
            cell: (pcx) => (
              <>
                <VpcLink vpcId={pcx.accepterVpcInfo.vpcId} currentVpcId={vpcId} />
                <span className="ml-1 text-fg-muted">({pcx.accepterVpcInfo.cidrBlock})</span>
              </>
            ),
          },
          {
            header: "Status",
            sortValue: (pcx) => pcx.status.code,
            cell: (pcx) => <PeeringStatusBadge code={pcx.status.code} />,
          },
        ]}
        rowActions={(pcx) => (
          <>
            {pcx.status.code === "pending-acceptance" && (
              <Button
                size="icon"
                variant="ghost"
                title="Accept"
                onClick={() => acceptMut.mutate(pcx.vpcPeeringConnectionId)}
              >
                <Check className="h-3.5 w-3.5" />
              </Button>
            )}
            {(pcx.status.code === "active" || pcx.status.code === "pending-acceptance") && (
              <Button
                size="icon"
                variant="ghost"
                className="text-fg-muted hover:text-danger"
                title="Delete"
                onClick={() => setDeleteTarget(pcx.vpcPeeringConnectionId)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </Button>
            )}
          </>
        )}
      />

      <CreatePeeringDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        vpcId={vpcId}
        isPending={createMut.isPending}
        onSubmit={(peerVpcId) => createMut.mutate({ vpcId, peerVpcId })}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Peering Connection"
        description={
          <>
            Delete peering connection <strong>{deleteTarget}</strong>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Endpoints Panel ──────────────────────────────────────────────────────

function EndpointsPanel({ vpcId }: { vpcId: string }) {
  const { data: endpoints = [], isLoading } = useQuery(ec2VpcEndpointsQueryOptions(vpcId))

  return (
    <ResourceTable
      variant="embedded"
      columnToggle={false}
      query={{ data: endpoints, isLoading }}
      noun="endpoints"
      emptyTitle="No endpoints"
      emptyDescription="This VPC has no VPC endpoints."
      rowKey={(ep) => ep.vpcEndpointId}
      columns={[
        {
          header: "Endpoint ID",
          sortValue: (ep) => ep.vpcEndpointId,
          cell: (ep) => ep.vpcEndpointId,
        },
        {
          header: "Service Name",
          cellClassName: "text-fg-muted",
          sortValue: (ep) => ep.serviceName,
          cell: (ep) => ep.serviceName,
        },
        { header: "Type", cell: (ep) => ep.vpcEndpointType },
        {
          header: "State",
          cell: (ep) => (
            <Badge variant={ep.state === "available" ? "success" : "default"}>{ep.state}</Badge>
          ),
        },
      ]}
    />
  )
}

// ─── Security Groups Panel ────────────────────────────────────────────────

function SecurityGroupsPanel({ vpcId }: { vpcId: string }) {
  const { data: allSGs = [], isLoading } = useQuery(ec2SecurityGroupsQueryOptions())
  const groups = allSGs.filter((sg) => sg.vpcId === vpcId)

  return (
    <ResourceTable
      variant="embedded"
      // Five columns, but the two rule counts and the description are all worth
      // reading side by side on a security-group audit — nothing here is
      // secondary enough to hide by default.
      columnToggle={false}
      query={{ data: groups, isLoading }}
      noun="security groups"
      emptyTitle="No security groups"
      emptyDescription="No security groups in this VPC."
      rowKey={(sg) => sg.groupId}
      columns={[
        { header: "Group ID", sortValue: (sg) => sg.groupId, cell: (sg) => sg.groupId },
        {
          header: "Name",
          cellClassName: "font-medium",
          sortValue: (sg) => sg.groupName,
          cell: (sg) => sg.groupName,
        },
        {
          header: "Description",
          prose: true,
          cellClassName: "max-w-xs truncate",
          cell: (sg) => sg.description,
        },
        {
          header: "Inbound Rules",
          sortValue: (sg) => sg.ipPermissions.length,
          cell: (sg) => <Badge variant="default">{sg.ipPermissions.length}</Badge>,
        },
        {
          header: "Outbound Rules",
          sortValue: (sg) => sg.ipPermissionsEgress.length,
          cell: (sg) => <Badge variant="default">{sg.ipPermissionsEgress.length}</Badge>,
        },
      ]}
    />
  )
}

// ─── Create Peering Dialog ────────────────────────────────────────────────

function CreatePeeringDialog({
  open,
  onClose,
  vpcId,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  vpcId: string
  isPending: boolean
  onSubmit: (peerVpcId: string) => void
}) {
  const [peerVpcId, setPeerVpcId] = useState("")
  const { data: vpcs = [] } = useQuery(ec2VpcsQueryOptions())
  const peerOptions = vpcs.filter((v) => v.vpcId !== vpcId)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          setPeerVpcId("")
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Peering Connection</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Requester VPC</label>
            <Input value={vpcId} disabled />
          </div>
          <div>
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Peer VPC</label>
            {peerOptions.length > 0 ? (
              <Combobox<{ value: string; label: string }>
                value={peerVpcId}
                onChange={(v) => setPeerVpcId(v)}
                items={peerOptions.map((v) => ({
                  value: v.vpcId,
                  label: `${v.vpcId} (${v.cidrBlock})`,
                }))}
                filterFn={(item, q) => item.label.toLowerCase().includes(q.toLowerCase())}
                getItemValue={(item) => item.value}
                renderItem={(item: { value: string; label: string }) => item.label}
                placeholder="Select peer VPC…"
              />
            ) : (
              <Input
                placeholder="vpc-xxxxxxxx"
                value={peerVpcId}
                onChange={(e) => setPeerVpcId(e.target.value)}
              />
            )}
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={isPending || !peerVpcId} onClick={() => onSubmit(peerVpcId)}>
            {isPending && <Spinner className="mr-2" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Shared helpers ───────────────────────────────────────────────────────

function PeeringStatusBadge({ code }: { code: string }) {
  const variant =
    code === "active"
      ? "success"
      : code === "pending-acceptance" || code === "provisioning"
        ? "warning"
        : code === "deleted" || code === "rejected" || code === "failed"
          ? "danger"
          : "default"
  return <Badge variant={variant}>{code}</Badge>
}

function VpcLink({ vpcId, currentVpcId }: { vpcId: string; currentVpcId: string }) {
  if (vpcId === currentVpcId) {
    return <span>{vpcId}</span>
  }
  return (
    <Link to="/ec2/vpc/$vpcId" params={{ vpcId }} className="text-accent hover:underline">
      {vpcId}
    </Link>
  )
}

// ─── Tags Panel ───────────────────────────────────────────────────────────

function TagsPanel({ tags }: { tags?: Array<{ key: string; value: string }> }) {
  return (
    <ResourceTable
      variant="embedded"
      columnToggle={false}
      query={{ data: tags ?? [], isLoading: false }}
      noun="tags"
      emptyTitle="No tags"
      emptyDescription="This VPC has no tags."
      rowKey={(tag) => tag.key}
      columns={[
        { header: "Key", sortValue: (tag) => tag.key, cell: (tag) => tag.key },
        { header: "Value", cellClassName: "text-fg-muted", cell: (tag) => tag.value },
      ]}
    />
  )
}
