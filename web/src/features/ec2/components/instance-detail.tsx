import { useState } from "react"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import {
  ec2InstanceDetailQueryOptions,
  ec2SecurityGroupsQueryOptions,
  ec2Keys,
  modifyInstanceTypeMutationOptions,
} from "@/features/ec2/data"
import { PageHeader, Spinner } from "@/components/ui/primitives"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { ApplicationOwnershipBanner } from "@/components/application-ownership-banner"
import { InstanceStateBadge } from "./instance-state-badge"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ResourceTable } from "@/components/ui/resource-table"
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Pencil } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { useToast } from "@/components/ui/toast"
import type { Ec2SecurityGroup, Ec2IpPermission } from "@/types"

export function InstanceDetail({ instanceId }: { instanceId: string }) {
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

  const sgDisplay = inst.securityGroups?.length
    ? inst.securityGroups.map((sg) => `${sg.groupName} (${sg.groupId})`).join(", ")
    : null

  return (
    <>
      <DefinitionList>
        <Definition label="Instance ID" value={inst.instanceId} />
        <Definition label="State" value={<InstanceStateBadge state={inst.state.name} />} />
        <Definition
          label="Instance Type"
          value={
            <span className="flex items-center gap-1.5">
              {inst.instanceType}
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
            </span>
          }
        />
        <Definition label="AMI ID" value={inst.imageId} />
        <Definition label="VPC ID" value={inst.vpcId} />
        <Definition label="Subnet ID" value={inst.subnetId} />
        <Definition
          label="Private IP"
          value={
            inst.privateIpAddress && (
              <CopyableValue value={inst.privateIpAddress} noun="private IP" code />
            )
          }
        />
        <Definition label="Security Groups" value={sgDisplay} />
        <Definition
          label="Launch Time"
          value={inst.launchTime ? new Date(inst.launchTime).toLocaleString() : null}
        />
        {(inst.state.name === "stopped" || inst.state.name === "terminated") && (
          <Definition label="State Reason" value={`Instance is ${inst.state.name}`} />
        )}
      </DefinitionList>

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
            <label className={cn(fieldLabel, "mb-1 block text-fg")}>Instance Type</label>
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
      <h3 className="font-mono text-sm font-semibold">
        {sg.groupName}{" "}
        <span className="font-mono text-xs font-normal text-fg-muted">({sg.groupId})</span>
      </h3>

      <div className="space-y-2">
        <h4 className="font-mono text-xs font-medium text-fg-muted">Inbound Rules</h4>
        {sg.ipPermissions.length === 0 ? (
          <p className="text-xs text-fg-muted">No inbound rules.</p>
        ) : (
          <RulesTable rules={sg.ipPermissions} direction="inbound" />
        )}
      </div>

      <div className="space-y-2">
        <h4 className="font-mono text-xs font-medium text-fg-muted">Outbound Rules</h4>
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
    <ResourceTable
      variant="embedded"
      query={{ data: rules, isLoading: false }}
      noun="rules"
      // The caller renders its own one-line "No inbound rules." above this, so
      // the table is only ever mounted with rows.
      emptyTitle={`No ${direction} rules.`}
      rowKey={(rule) => `${rule.ipProtocol}-${rule.fromPort ?? ""}-${rule.toPort ?? ""}`}
      columns={[
        {
          header: "Protocol",
          sortValue: (rule) => rule.ipProtocol,
          cell: (rule) => (rule.ipProtocol === "-1" ? "All" : rule.ipProtocol.toUpperCase()),
        },
        {
          header: "Port Range",
          sortValue: (rule) => rule.fromPort ?? -1,
          cell: (rule) => portRangeLabel(rule),
        },
        {
          header: sourceLabel,
          cell: (rule) => rule.ipRanges?.map((r) => r.cidrIp).join(", ") || "—",
        },
      ]}
    />
  )
}

/** "All", a single port, or a `from–to` span. */
function portRangeLabel(rule: Ec2IpPermission): string {
  if (rule.ipProtocol === "-1") return "All"
  if (rule.fromPort === rule.toPort) return String(rule.fromPort ?? "—")
  return `${rule.fromPort ?? "—"}–${rule.toPort ?? "—"}`
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
  return (
    <div className="space-y-6">
      <DefinitionList>
        <Definition
          label="VPC ID"
          value={inst.vpcId && <CopyableValue value={inst.vpcId} noun="VPC ID" />}
        />
        <Definition
          label="Subnet ID"
          value={inst.subnetId && <CopyableValue value={inst.subnetId} noun="subnet ID" />}
        />
        <Definition
          label="Private IP Address"
          value={
            inst.privateIpAddress && (
              <CopyableValue value={inst.privateIpAddress} noun="private IP" code />
            )
          }
        />
        <Definition label="Private DNS Name" value={null} />
        <Definition label="Network Interface ID" value={null} />
      </DefinitionList>
    </div>
  )
}

// ─── Tags Panel ───────────────────────────────────────────────────────────

function TagsPanel({ tags }: { tags?: Array<{ key: string; value: string }> }) {
  if (!tags?.length) {
    return <p className="text-sm text-fg-muted">No tags configured</p>
  }

  return (
    <ResourceTable
      variant="embedded"
      query={{ data: tags, isLoading: false }}
      noun="tags"
      emptyTitle="No tags configured"
      rowKey={(tag) => tag.key}
      columns={[
        { header: "Key", sortValue: (tag) => tag.key, cell: (tag) => tag.key },
        { header: "Value", cell: (tag) => tag.value },
      ]}
    />
  )
}

// ─── Shared ───────────────────────────────────────────────────────────────

/**
 * A definition value with a copy affordance beside it. `code` boxes the value,
 * for IPs. The button is `tone="inline"` because it sits against the value
 * rather than in a row of controls.
 */
function CopyableValue({
  value,
  noun,
  code = false,
}: {
  value: string
  noun: string
  code?: boolean
}) {
  return (
    <span className="flex items-center gap-1.5">
      {code ? <code className="rounded bg-bg-muted px-2 py-0.5">{value}</code> : value}
      <CopyButton value={value} noun={noun} tone="inline" />
    </span>
  )
}
