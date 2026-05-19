import { useState } from "react"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import {
  ec2InstanceDetailQueryOptions,
  ec2SecurityGroupsQueryOptions,
  ec2Keys,
  modifyInstanceTypeMutationOptions,
} from "@/features/ec2/data"
import { PageHeader, Spinner, Breadcrumb } from "@/components/ui/primitives"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { Badge } from "@/components/ui/badge"
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
import { Input } from "@/components/ui/input"
import { Copy, Pencil } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useToast } from "@/components/ui/toast"
import type { Ec2SecurityGroup, Ec2IpPermission } from "@/types"

export function InstanceDetail({ instanceId }: { instanceId: string }) {
  const navigate = useNavigate()
  const [activeTab, setActiveTab] = useState("overview")

  const { data: inst, isLoading } = useQuery(ec2InstanceDetailQueryOptions(instanceId))

  if (isLoading) {
    return (
      <div className="flex justify-center py-32">
        <Spinner className="h-6 w-6" />
      </div>
    )
  }

  if (!inst) return null

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title={inst.instanceId}
        description={
          <span className="flex items-center gap-2">
            <span className="text-sm text-fg-muted">{inst.instanceType}</span>
            <InstanceStateBadge state={inst.state.name} />
          </span>
        }
        breadcrumb={
          <Breadcrumb
            items={[
              { label: "EC2 / VPC", onClick: () => navigate({ to: "/ec2" }) },
              { label: "Instances", onClick: () => navigate({ to: "/ec2" }) },
              { label: inst.instanceId },
            ]}
          />
        }
      />

      <ApplicationOwnershipBanner candidates={[inst.instanceId]} />

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="overview">Overview</Tab>
          <Tab id="networking">Networking</Tab>
          <Tab id="security">Security</Tab>
          <Tab id="tags">Tags</Tab>
        </TabList>

        <TabPanel id="overview" className="pt-4">
          <OverviewPanel inst={inst} />
        </TabPanel>

        <TabPanel id="networking" className="pt-4">
          <NetworkingPanel inst={inst} />
        </TabPanel>

        <TabPanel id="security" className="pt-4">
          <SecurityPanel securityGroups={inst.securityGroups} />
        </TabPanel>

        <TabPanel id="tags" className="pt-4">
          <TagsPanel tags={inst.tags} />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── Overview Panel ───────────────────────────────────────────────────────

function OverviewPanel({
  inst,
}: {
  inst: {
    instanceId: string
    imageId: string
    instanceType: string
    state: { name: string }
    privateIpAddress?: string
    vpcId?: string
    subnetId?: string
    securityGroups?: Array<{ groupId: string; groupName: string }>
    launchTime?: string
  }
}) {
  const { toast } = useToast()
  const [showEditType, setShowEditType] = useState(false)
  const queryClient = useQueryClient()

  const modifyMut = useMutation({
    ...modifyInstanceTypeMutationOptions(),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ec2Keys.instances() })
      toast({ title: "Instance type updated", variant: "success" })
      setShowEditType(false)
    },
    onError: () => toast({ title: "Failed to update instance type", variant: "danger" }),
  })

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text)
    toast({ title: "Copied!", variant: "success" })
  }

  const sgDisplay = inst.securityGroups?.length
    ? inst.securityGroups.map((sg) => `${sg.groupName} (${sg.groupId})`).join(", ")
    : "—"

  return (
    <>
      <div className="grid grid-cols-2 gap-x-8 gap-y-3">
        <InfoRow label="Instance ID" value={inst.instanceId} />
        <div className="flex flex-col gap-0.5">
          <span className="text-xs text-fg-muted">State</span>
          <InstanceStateBadge state={inst.state.name} />
        </div>
        <div className="flex flex-col gap-0.5">
          <span className="text-xs text-fg-muted">Instance Type</span>
          <div className="flex items-center gap-1.5">
            <span className="text-sm text-fg">{inst.instanceType}</span>
            {inst.state.name === "stopped" && (
              <Button
                size="icon"
                variant="ghost"
                className="h-6 w-6 text-fg-muted"
                title="Change instance type"
                onClick={() => setShowEditType(true)}
              >
                <Pencil className="h-3 w-3" />
              </Button>
            )}
          </div>
        </div>
        <InfoRow label="AMI ID" value={inst.imageId} />
        <InfoRow label="VPC ID" value={inst.vpcId ?? "—"} />
        <InfoRow label="Subnet ID" value={inst.subnetId ?? "—"} />
        <div className="flex flex-col gap-0.5">
          <span className="text-xs text-fg-muted">Private IP</span>
          {inst.privateIpAddress ? (
            <div className="flex items-center gap-1.5">
              <code className="rounded bg-bg-muted px-2 py-0.5 font-mono text-sm">
                {inst.privateIpAddress}
              </code>
              <Button
                size="icon"
                variant="ghost"
                className="h-7 w-7"
                onClick={() => copyToClipboard(inst.privateIpAddress!)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
          ) : (
            <span className="text-sm text-fg">—</span>
          )}
        </div>
        <InfoRow label="Security Groups" value={sgDisplay} />
        <InfoRow
          label="Launch Time"
          value={inst.launchTime ? new Date(inst.launchTime).toLocaleString() : "—"}
        />
        {(inst.state.name === "stopped" || inst.state.name === "terminated") && (
          <InfoRow label="State Reason" value={`Instance is ${inst.state.name}`} />
        )}
      </div>

      <EditInstanceTypeDialog
        open={showEditType}
        onClose={() => setShowEditType(false)}
        currentType={inst.instanceType}
        isPending={modifyMut.isPending}
        onSubmit={(instanceType) => modifyMut.mutate({ instanceId: inst.instanceId, instanceType })}
      />
    </>
  )
}

function EditInstanceTypeDialog({
  open,
  onClose,
  currentType,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  currentType: string
  isPending: boolean
  onSubmit: (instanceType: string) => void
}) {
  const [instanceType, setInstanceType] = useState(currentType)

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          setInstanceType(currentType)
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change Instance Type</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <p className="text-sm text-fg-muted">Instance must be stopped to change its type.</p>
          <div>
            <label className="mb-1 block text-sm font-medium text-fg">Instance Type</label>
            <Input
              placeholder="e.g. t3.medium"
              value={instanceType}
              onChange={(e) => setInstanceType(e.target.value)}
            />
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={isPending || !instanceType} onClick={() => onSubmit(instanceType)}>
            {isPending && <Spinner className="mr-2" />}
            Apply
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Security Panel ───────────────────────────────────────────────────────

function SecurityPanel({
  securityGroups,
}: {
  securityGroups?: Array<{ groupId: string; groupName: string }>
}) {
  const { data: allSGs = [], isLoading } = useQuery(ec2SecurityGroupsQueryOptions())

  if (isLoading) {
    return (
      <div className="flex justify-center py-8">
        <Spinner className="h-5 w-5" />
      </div>
    )
  }

  if (!securityGroups?.length) {
    return <p className="text-sm text-fg-muted">No security groups attached.</p>
  }

  // Match instance SG refs to full SG details.
  const sgIdSet = new Set(securityGroups.map((sg) => sg.groupId))
  const matched = allSGs.filter((sg) => sgIdSet.has(sg.groupId))

  if (matched.length === 0) {
    return (
      <div className="space-y-2">
        <p className="text-sm text-fg-muted">
          Security groups:{" "}
          {securityGroups.map((sg) => `${sg.groupName} (${sg.groupId})`).join(", ")}
        </p>
        <p className="text-xs text-fg-muted">Rule details not available.</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {matched.map((sg) => (
        <SGRulesSection key={sg.groupId} sg={sg} />
      ))}
    </div>
  )
}

function SGRulesSection({ sg }: { sg: Ec2SecurityGroup }) {
  return (
    <div className="space-y-3">
      <h3 className="text-sm font-semibold">
        {sg.groupName}{" "}
        <span className="font-mono text-xs font-normal text-fg-muted">({sg.groupId})</span>
      </h3>

      <div className="space-y-2">
        <h4 className="text-xs font-medium text-fg-muted">Inbound Rules</h4>
        {sg.ipPermissions.length === 0 ? (
          <p className="text-xs text-fg-muted">No inbound rules.</p>
        ) : (
          <RulesTable rules={sg.ipPermissions} direction="inbound" />
        )}
      </div>

      <div className="space-y-2">
        <h4 className="text-xs font-medium text-fg-muted">Outbound Rules</h4>
        {sg.ipPermissionsEgress.length === 0 ? (
          <p className="text-xs text-fg-muted">No outbound rules.</p>
        ) : (
          <RulesTable rules={sg.ipPermissionsEgress} direction="outbound" />
        )}
      </div>
    </div>
  )
}

function RulesTable({
  rules,
  direction,
}: {
  rules: Ec2IpPermission[]
  direction: "inbound" | "outbound"
}) {
  const sourceLabel = direction === "inbound" ? "Source" : "Destination"

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Protocol</TableHead>
          <TableHead>Port Range</TableHead>
          <TableHead>{sourceLabel}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rules.map((rule, idx) => {
          const protocol = rule.ipProtocol === "-1" ? "All" : rule.ipProtocol.toUpperCase()
          const portRange =
            rule.ipProtocol === "-1"
              ? "All"
              : rule.fromPort === rule.toPort
                ? String(rule.fromPort ?? "—")
                : `${rule.fromPort ?? "—"}–${rule.toPort ?? "—"}`
          const source = rule.ipRanges?.map((r) => r.cidrIp).join(", ") || "—"

          return (
            <TableRow key={idx}>
              <TableCell className="text-xs">{protocol}</TableCell>
              <TableCell className="font-mono text-xs">{portRange}</TableCell>
              <TableCell className="font-mono text-xs">{source}</TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

// ─── Networking Panel ─────────────────────────────────────────────────────

function NetworkingPanel({
  inst,
}: {
  inst: {
    vpcId?: string
    subnetId?: string
    privateIpAddress?: string
  }
}) {
  const { toast } = useToast()

  const copyToClipboard = (text: string) => {
    void navigator.clipboard.writeText(text)
    toast({ title: "Copied!", variant: "success" })
  }

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 gap-x-8 gap-y-3">
        {inst.vpcId ? (
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-fg-muted">VPC ID</span>
            <div className="flex items-center gap-1.5">
              <span className="text-sm text-fg">{inst.vpcId}</span>
              <Button
                size="icon"
                variant="ghost"
                className="h-7 w-7"
                onClick={() => copyToClipboard(inst.vpcId!)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        ) : (
          <InfoRow label="VPC ID" value="—" />
        )}

        {inst.subnetId ? (
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-fg-muted">Subnet ID</span>
            <div className="flex items-center gap-1.5">
              <span className="text-sm text-fg">{inst.subnetId}</span>
              <Button
                size="icon"
                variant="ghost"
                className="h-7 w-7"
                onClick={() => copyToClipboard(inst.subnetId!)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        ) : (
          <InfoRow label="Subnet ID" value="—" />
        )}

        {inst.privateIpAddress ? (
          <div className="flex flex-col gap-0.5">
            <span className="text-xs text-fg-muted">Private IP Address</span>
            <div className="flex items-center gap-1.5">
              <code className="rounded bg-bg-muted px-2 py-0.5 font-mono text-sm">
                {inst.privateIpAddress}
              </code>
              <Button
                size="icon"
                variant="ghost"
                className="h-7 w-7"
                onClick={() => copyToClipboard(inst.privateIpAddress!)}
              >
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        ) : (
          <InfoRow label="Private IP Address" value="—" />
        )}

        <InfoRow label="Private DNS Name" value="—" />
        <InfoRow label="Network Interface ID" value="—" />
      </div>
    </div>
  )
}

// ─── Tags Panel ───────────────────────────────────────────────────────────

function TagsPanel({ tags }: { tags?: Array<{ key: string; value: string }> }) {
  if (!tags?.length) {
    return <p className="text-sm text-fg-muted">No tags configured</p>
  }

  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Key</TableHead>
          <TableHead>Value</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tags.map((tag) => (
          <TableRow key={tag.key}>
            <TableCell className="font-mono text-sm">{tag.key}</TableCell>
            <TableCell className="text-sm">{tag.value}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// ─── Shared ───────────────────────────────────────────────────────────────

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className="text-sm text-fg">{value}</span>
    </div>
  )
}

function InstanceStateBadge({ state }: { state: string }) {
  const variant =
    state === "running"
      ? "success"
      : state === "pending" || state === "shutting-down"
        ? "warning"
        : state === "terminated"
          ? "danger"
          : state === "stopped"
            ? "default"
            : "default"
  return <Badge variant={variant}>{state}</Badge>
}
