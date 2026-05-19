import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { Cpu, Plus, Trash2, RefreshCw, Play, Square, Link as LinkIcon } from "lucide-react"
import type { Ec2ElasticIp } from "@/types"
import {
  ec2InstancesQueryOptions,
  ec2VpcsQueryOptions,
  ec2SecurityGroupsQueryOptions,
  ec2ElasticIpsQueryOptions,
  ec2NatGatewaysQueryOptions,
  ec2Keys,
  runInstancesMutationOptions,
  terminateInstancesMutationOptions,
  startInstancesMutationOptions,
  stopInstancesMutationOptions,
  createVpcMutationOptions,
  deleteVpcMutationOptions,
  createSecurityGroupMutationOptions,
  deleteSecurityGroupMutationOptions,
  allocateAddressMutationOptions,
  releaseAddressMutationOptions,
  associateAddressMutationOptions,
  disassociateAddressMutationOptions,
  createNatGatewayMutationOptions,
  deleteNatGatewayMutationOptions,
} from "@/features/ec2/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Combobox } from "@/components/ui/combobox"
import { FormField, fieldError } from "@/components/ui/form"
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
import { ConfirmDialog } from "@/components/ui/confirm-dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabList, Tab, TabPanel } from "@/components/ui/tabs"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { cn } from "@/lib/utils"

export function Ec2Dashboard() {
  const [activeTab, setActiveTab] = useState("instances")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="EC2 / VPC"
        description="Instances, VPCs, and Security Groups"
        actions={
          <ServiceDocsButton
            service="ec2"
            label="EC2 / VPC"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
        }
      />

      <Tabs selectedKey={activeTab} onSelectionChange={setActiveTab}>
        <TabList>
          <Tab id="instances">Instances</Tab>
          <Tab id="vpcs">VPCs</Tab>
          <Tab id="security-groups">Security Groups</Tab>
          <Tab id="elastic-ips">Elastic IPs</Tab>
          <Tab id="nat-gateways">NAT Gateways</Tab>
        </TabList>

        <TabPanel id="instances" className="pt-4">
          <InstancesPanel />
        </TabPanel>
        <TabPanel id="vpcs" className="pt-4">
          <VpcsPanel />
        </TabPanel>
        <TabPanel id="security-groups" className="pt-4">
          <SecurityGroupsPanel />
        </TabPanel>
        <TabPanel id="elastic-ips" className="pt-4">
          <ElasticIpsPanel />
        </TabPanel>
        <TabPanel id="nat-gateways" className="pt-4">
          <NatGatewaysPanel />
        </TabPanel>
      </Tabs>
    </div>
  )
}

// ─── NAT Gateway state badge ────────────────────────────────────────────────

function NatGatewayStateBadge({ state }: { state: string }) {
  const variant =
    state === "available"
      ? "success"
      : state === "pending"
        ? "warning"
        : state === "deleting" || state === "deleted"
          ? "default"
          : state === "failed"
            ? "danger"
            : "default"
  return <Badge variant={variant}>{state}</Badge>
}

// ─── Instance state badge ─────────────────────────────────────────────────

function InstanceStateBadge({ state }: { state: string }) {
  const variant =
    state === "running"
      ? "success"
      : state === "pending" || state === "stopping" || state === "shutting-down"
        ? "warning"
        : state === "terminated"
          ? "default"
          : state === "stopped"
            ? "danger"
            : "default"
  return <Badge variant={variant}>{state}</Badge>
}

// ─── Instances Panel ──────────────────────────────────────────────────────

function InstancesPanel() {
  const [showLaunch, setShowLaunch] = useState(false)
  const [terminateTarget, setTerminateTarget] = useState<string>()
  const [stateFilter, setStateFilter] = useState<string>("all")

  const {
    data: instances = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ec2InstancesQueryOptions())

  const filtered =
    stateFilter === "all" ? instances : instances.filter((i) => i.state.name === stateFilter)

  const terminateMut = useResourceMutation({
    options: terminateInstancesMutationOptions(),
    invalidateKeys: [ec2Keys.instances()],
    successTitle: "Instance terminated",
    successVariant: "default",
    onSuccess: () => setTerminateTarget(undefined),
  })

  const startMut = useResourceMutation({
    options: startInstancesMutationOptions(),
    invalidateKeys: [ec2Keys.instances()],
    successTitle: "Instance started",
  })

  const stopMut = useResourceMutation({
    options: stopInstancesMutationOptions(),
    invalidateKeys: [ec2Keys.instances()],
    successTitle: "Instance stopped",
    successVariant: "default",
  })

  const runMut = useResourceMutation({
    options: runInstancesMutationOptions(),
    invalidateKeys: [ec2Keys.instances()],
    successTitle: "Instance launched",
    onSuccess: () => setShowLaunch(false),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowLaunch(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Launch Instance
        </Button>
      </div>

      {instances.length > 0 && (
        <div className="flex items-center gap-1.5">
          {["all", "running", "stopped", "terminated"].map((s) => (
            <Button
              key={s}
              size="sm"
              variant={stateFilter === s ? "default" : "secondary"}
              onClick={() => setStateFilter(s)}
              className="h-7 text-xs capitalize"
            >
              {s}
            </Button>
          ))}
        </div>
      )}

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : instances.length === 0 ? (
        <EmptyState
          icon={<Cpu className="h-10 w-10" />}
          title="No instances"
          description="Launch an instance to get started."
          action={
            <Button onClick={() => setShowLaunch(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Launch Instance
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Instance ID</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Private IP</TableHead>
              <TableHead>VPC ID</TableHead>
              <TableHead>Launch Time</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((i) => (
              <TableRow key={i.instanceId}>
                <TableCell className="font-mono text-xs">
                  <Link
                    to="/ec2/$instanceId"
                    params={{ instanceId: i.instanceId }}
                    className="text-fg-accent hover:underline"
                  >
                    {i.instanceId}
                  </Link>
                </TableCell>
                <TableCell>
                  <InstanceStateBadge state={i.state.name} />
                </TableCell>
                <TableCell className="text-sm">{i.instanceType}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {i.privateIpAddress ?? "—"}
                </TableCell>
                <TableCell className="text-xs text-fg-muted">{i.vpcId ?? "—"}</TableCell>
                <TableCell className="text-xs text-fg-muted">
                  {i.launchTime ? new Date(i.launchTime).toLocaleString() : "—"}
                </TableCell>
                <TableCell>
                  <div className="flex gap-1">
                    {i.state.name === "stopped" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        title="Start"
                        onClick={() => startMut.mutate([i.instanceId])}
                      >
                        <Play className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    {i.state.name === "running" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        title="Stop"
                        onClick={() => stopMut.mutate([i.instanceId])}
                      >
                        <Square className="h-3.5 w-3.5" />
                      </Button>
                    )}
                    {i.state.name !== "terminated" && i.state.name !== "shutting-down" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        className="text-fg-muted hover:text-danger"
                        title="Terminate"
                        onClick={() => setTerminateTarget(i.instanceId)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <LaunchInstanceDialog
        open={showLaunch}
        onClose={() => setShowLaunch(false)}
        isPending={runMut.isPending}
        onSubmit={(opts) => runMut.mutate(opts)}
      />

      <ConfirmDialog
        open={!!terminateTarget}
        onOpenChange={(v) => !v && setTerminateTarget(undefined)}
        title="Terminate Instance"
        description={
          <>
            Terminate instance <strong>{terminateTarget}</strong>? This cannot be undone.
          </>
        }
        isPending={terminateMut.isPending}
        onConfirm={() => terminateTarget && terminateMut.mutate([terminateTarget])}
      />
    </div>
  )
}

// ─── VPCs Panel ───────────────────────────────────────────────────────────

function VpcsPanel() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  const { data: vpcs = [], isLoading, isFetching, refetch } = useQuery(ec2VpcsQueryOptions())

  const createMut = useResourceMutation({
    options: createVpcMutationOptions(),
    invalidateKeys: [ec2Keys.vpcs()],
    successTitle: "VPC created",
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteVpcMutationOptions(),
    invalidateKeys: [ec2Keys.vpcs()],
    successTitle: "VPC deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Create VPC
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : vpcs.length === 0 ? (
        <EmptyState
          title="No VPCs"
          description="Create a VPC to set up networking."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create VPC
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>VPC ID</TableHead>
              <TableHead>CIDR</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Default</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {vpcs.map((v) => (
              <TableRow key={v.vpcId}>
                <TableCell className="font-mono text-xs">
                  <Link
                    to="/ec2/vpc/$vpcId"
                    params={{ vpcId: v.vpcId }}
                    className="text-fg-accent hover:underline"
                  >
                    {v.vpcId}
                  </Link>
                </TableCell>
                <TableCell className="font-mono text-xs">{v.cidrBlock}</TableCell>
                <TableCell>
                  <Badge variant={v.state === "available" ? "success" : "warning"}>{v.state}</Badge>
                </TableCell>
                <TableCell>{v.isDefault ? "Yes" : "No"}</TableCell>
                <TableCell>
                  {!v.isDefault && (
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      onClick={() => setDeleteTarget(v.vpcId)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateVpcDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(cidr) => createMut.mutate(cidr)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete VPC"
        description={
          <>
            Permanently delete VPC <strong>{deleteTarget}</strong>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Security Groups Panel ────────────────────────────────────────────────

function SecurityGroupsPanel() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  const {
    data: groups = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ec2SecurityGroupsQueryOptions())

  const createMut = useResourceMutation({
    options: createSecurityGroupMutationOptions(),
    invalidateKeys: [ec2Keys.securityGroups()],
    successTitle: "Security group created",
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteSecurityGroupMutationOptions(),
    invalidateKeys: [ec2Keys.securityGroups()],
    successTitle: "Security group deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Create Security Group
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : groups.length === 0 ? (
        <EmptyState
          title="No security groups"
          description="Create a security group to manage access."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create Security Group
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Group ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>VPC ID</TableHead>
              <TableHead>Inbound Rules</TableHead>
              <TableHead>Outbound Rules</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {groups.map((sg) => (
              <TableRow key={sg.groupId}>
                <TableCell className="font-mono text-xs">{sg.groupId}</TableCell>
                <TableCell className="font-medium">{sg.groupName}</TableCell>
                <TableCell className="max-w-xs truncate text-sm text-fg-muted">
                  {sg.description}
                </TableCell>
                <TableCell className="text-xs text-fg-muted">{sg.vpcId ?? "—"}</TableCell>
                <TableCell>
                  <Badge variant="default">{sg.ipPermissions.length}</Badge>
                </TableCell>
                <TableCell>
                  <Badge variant="default">{sg.ipPermissionsEgress.length}</Badge>
                </TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={() => setDeleteTarget(sg.groupId)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <CreateSecurityGroupDialog
        open={showCreate}
        onClose={() => setShowCreate(false)}
        isPending={createMut.isPending}
        onSubmit={(opts) => createMut.mutate(opts)}
      />

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete Security Group"
        description={
          <>
            Permanently delete security group <strong>{deleteTarget}</strong>?
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

// ─── Launch Instance Dialog ───────────────────────────────────────────────

const launchSchema = z.object({
  imageId: z.string().min(1, "AMI ID is required"),
  instanceType: z.string().min(1, "Instance type is required"),
  count: z.number().int().min(1).max(10),
})

function LaunchInstanceDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (opts: {
    imageId: string
    instanceType: string
    minCount: number
    maxCount: number
  }) => void
}) {
  const form = useForm({
    validators: { onChange: launchSchema },
    defaultValues: { imageId: "ami-12345678", instanceType: "t2.micro", count: 1 },
    onSubmit: ({ value }) =>
      onSubmit({
        imageId: value.imageId,
        instanceType: value.instanceType,
        minCount: value.count,
        maxCount: value.count,
      }),
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          form.reset()
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Launch Instance</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <form.Field name="imageId">
              {(field) => (
                <FormField
                  label="AMI ID"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="ami-12345678"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="instanceType">
              {(field) => (
                <FormField
                  label="Instance Type"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Combobox<{ value: string }>
                    value={field.state.value}
                    onChange={(v) => field.handleChange(v)}
                    items={[
                      { value: "t3.micro" },
                      { value: "t3.small" },
                      { value: "t3.medium" },
                      { value: "m5.large" },
                      { value: "m5.xlarge" },
                    ]}
                    filterFn={(item, q) => item.value.toLowerCase().includes(q.toLowerCase())}
                    getItemValue={(item) => item.value}
                    renderItem={(item) => item.value}
                    allowCustom
                    placeholder="Select instance type…"
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="count">
              {(field) => (
                <FormField
                  label="Count"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    type="number"
                    min={1}
                    max={10}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(parseInt(e.target.value) || 1)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isPending}>
              {isPending && <Spinner className="mr-2" />}
              Launch
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ─── Create VPC Dialog ────────────────────────────────────────────────────

function CreateVpcDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (cidr: string) => void
}) {
  const [cidr, setCidr] = useState("10.0.0.0/16")

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) onClose()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create VPC</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <div>
            <label className="mb-1 block text-sm font-medium text-fg">CIDR Block</label>
            <Input
              placeholder="10.0.0.0/16"
              value={cidr}
              onChange={(e) => setCidr(e.target.value)}
            />
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={isPending || !cidr} onClick={() => onSubmit(cidr)}>
            {isPending && <Spinner className="mr-2" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── Create Security Group Dialog ─────────────────────────────────────────

const sgSchema = z.object({
  groupName: z.string().min(1, "Name is required"),
  description: z.string().min(1, "Description is required"),
  vpcId: z.string(),
})

function CreateSecurityGroupDialog({
  open,
  onClose,
  isPending,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  isPending: boolean
  onSubmit: (opts: { groupName: string; description: string; vpcId?: string }) => void
}) {
  const form = useForm({
    validators: { onChange: sgSchema },
    defaultValues: { groupName: "", description: "", vpcId: "" },
    onSubmit: ({ value }) =>
      onSubmit({
        groupName: value.groupName,
        description: value.description,
        vpcId: value.vpcId || undefined,
      }),
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          form.reset()
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Security Group</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <form.Field name="groupName">
              {(field) => (
                <FormField
                  label="Group Name"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="my-sg"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="description">
              {(field) => (
                <FormField
                  label="Description"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="Security group description"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
            <form.Field name="vpcId">
              {(field) => (
                <FormField
                  label="VPC ID (optional)"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    placeholder="vpc-xxxxxxxx"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    onBlur={field.handleBlur}
                  />
                </FormField>
              )}
            </form.Field>
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={onClose}>
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

// ─── Elastic IPs Panel ────────────────────────────────────────────────────

function ElasticIpsPanel() {
  const [associateTarget, setAssociateTarget] = useState<Ec2ElasticIp>()
  const [releaseTarget, setReleaseTarget] = useState<string>()

  const { data: eips = [], isLoading, isFetching, refetch } = useQuery(ec2ElasticIpsQueryOptions())

  const { data: instances = [] } = useQuery(ec2InstancesQueryOptions())

  const allocateMut = useResourceMutation({
    options: allocateAddressMutationOptions(),
    invalidateKeys: [ec2Keys.elasticIps()],
    successTitle: "Elastic IP allocated",
  })

  const releaseMut = useResourceMutation({
    options: releaseAddressMutationOptions(),
    invalidateKeys: [ec2Keys.elasticIps()],
    successTitle: "Elastic IP released",
    successVariant: "default",
    onSuccess: () => setReleaseTarget(undefined),
  })

  const associateMut = useResourceMutation({
    options: associateAddressMutationOptions(),
    invalidateKeys: [ec2Keys.elasticIps()],
    successTitle: "Address associated",
    onSuccess: () => setAssociateTarget(undefined),
  })

  const disassociateMut = useResourceMutation({
    options: disassociateAddressMutationOptions(),
    invalidateKeys: [ec2Keys.elasticIps()],
    successTitle: "Address disassociated",
    successVariant: "default",
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button
          size="sm"
          onClick={() => allocateMut.mutate(undefined)}
          disabled={allocateMut.isPending}
        >
          {allocateMut.isPending ? (
            <Spinner className="mr-2" />
          ) : (
            <Plus className="mr-1.5 h-3.5 w-3.5" />
          )}
          Allocate Address
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : eips.length === 0 ? (
        <EmptyState
          title="No Elastic IPs"
          description="Allocate an Elastic IP to reserve a static public address."
          action={
            <Button onClick={() => allocateMut.mutate(undefined)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Allocate Address
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Allocation ID</TableHead>
              <TableHead>Public IP</TableHead>
              <TableHead>Domain</TableHead>
              <TableHead>Associated Instance</TableHead>
              <TableHead>Private IP</TableHead>
              <TableHead className="w-24" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {eips.map((eip) => (
              <TableRow key={eip.allocationId}>
                <TableCell className="font-mono text-xs">{eip.allocationId}</TableCell>
                <TableCell className="font-mono text-xs">{eip.publicIp}</TableCell>
                <TableCell>
                  <Badge variant="default">{eip.domain}</Badge>
                </TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {eip.instanceId ?? "—"}
                </TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {eip.privateIpAddress ?? "—"}
                </TableCell>
                <TableCell>
                  <div className="flex items-center gap-1">
                    {eip.associationId ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-xs text-fg-muted hover:text-warning"
                        onClick={() => disassociateMut.mutate(eip.associationId!)}
                        disabled={disassociateMut.isPending}
                      >
                        Disassociate
                      </Button>
                    ) : (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-xs"
                        onClick={() => setAssociateTarget(eip)}
                      >
                        <LinkIcon className="mr-1 h-3 w-3" />
                        Associate
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      onClick={() => setReleaseTarget(eip.allocationId)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {associateTarget && (
        <AssociateAddressDialog
          eip={associateTarget}
          instances={instances.filter((i) => i.state.name === "running")}
          isPending={associateMut.isPending}
          onClose={() => setAssociateTarget(undefined)}
          onSubmit={(params) => associateMut.mutate(params)}
        />
      )}

      <ConfirmDialog
        open={!!releaseTarget}
        onOpenChange={(v) => !v && setReleaseTarget(undefined)}
        title="Release Elastic IP"
        description={
          <>
            Release Elastic IP <strong>{releaseTarget}</strong>? This address will be returned to
            the AWS pool.
          </>
        }
        isPending={releaseMut.isPending}
        onConfirm={() => releaseTarget && releaseMut.mutate(releaseTarget)}
      />
    </div>
  )
}

function AssociateAddressDialog({
  eip,
  instances,
  isPending,
  onClose,
  onSubmit,
}: {
  eip: Ec2ElasticIp
  instances: Array<{ instanceId: string; state: { name: string } }>
  isPending: boolean
  onClose: () => void
  onSubmit: (params: { allocationId: string; instanceId: string }) => void
}) {
  const [instanceId, setInstanceId] = useState("")

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Associate Elastic IP</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <p className="text-sm text-fg-muted">
            Associate <span className="font-mono text-xs">{eip.publicIp}</span> with a running
            instance.
          </p>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium text-fg-muted">Instance</label>
            <select
              value={instanceId}
              onChange={(e) => setInstanceId(e.target.value)}
              className="flex h-8 w-full rounded-md border border-border bg-bg px-3 py-1 text-sm text-fg focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"
            >
              <option value="">Select instance…</option>
              {instances.map((i) => (
                <option key={i.instanceId} value={i.instanceId}>
                  {i.instanceId}
                </option>
              ))}
            </select>
          </div>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={!instanceId || isPending}
            onClick={() => onSubmit({ allocationId: eip.allocationId, instanceId })}
          >
            {isPending && <Spinner className="mr-2" />}
            Associate
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ─── NAT Gateways Panel ───────────────────────────────────────────────────

function NatGatewaysPanel() {
  const [showCreate, setShowCreate] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string>()

  const {
    data: natGateways = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(ec2NatGatewaysQueryOptions())

  const createMut = useResourceMutation({
    options: createNatGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.natGateways()],
    successTitle: "NAT Gateway created",
    onSuccess: () => setShowCreate(false),
  })

  const deleteMut = useResourceMutation({
    options: deleteNatGatewayMutationOptions(),
    invalidateKeys: [ec2Keys.natGateways()],
    successTitle: "NAT Gateway deleted",
    successVariant: "default",
    onSuccess: () => setDeleteTarget(undefined),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
          Refresh
        </Button>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <Plus className="mr-1.5 h-3.5 w-3.5" />
          Create NAT Gateway
        </Button>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner className="h-6 w-6" />
        </div>
      ) : natGateways.length === 0 ? (
        <EmptyState
          title="No NAT Gateways"
          description="Create a NAT Gateway to allow private subnet internet access."
          action={
            <Button onClick={() => setShowCreate(true)}>
              <Plus className="mr-1.5 h-3.5 w-3.5" />
              Create NAT Gateway
            </Button>
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>NAT Gateway ID</TableHead>
              <TableHead>State</TableHead>
              <TableHead>VPC ID</TableHead>
              <TableHead>Subnet ID</TableHead>
              <TableHead>Public IP</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {natGateways.map((ngw) => (
              <TableRow key={ngw.natGatewayId}>
                <TableCell className="font-mono text-xs">{ngw.natGatewayId}</TableCell>
                <TableCell>
                  <NatGatewayStateBadge state={ngw.state} />
                </TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{ngw.vpcId}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">{ngw.subnetId}</TableCell>
                <TableCell className="font-mono text-xs text-fg-muted">
                  {ngw.publicIp ?? "—"}
                </TableCell>
                <TableCell className="text-xs text-fg-muted">
                  {ngw.createTime ? new Date(ngw.createTime).toLocaleString() : "—"}
                </TableCell>
                <TableCell>
                  <Button
                    size="icon"
                    variant="ghost"
                    className="text-fg-muted hover:text-danger"
                    onClick={() => setDeleteTarget(ngw.natGatewayId)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {showCreate && (
        <CreateNatGatewayDialog
          isPending={createMut.isPending}
          onClose={() => setShowCreate(false)}
          onSubmit={(params) => createMut.mutate(params)}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(v) => !v && setDeleteTarget(undefined)}
        title="Delete NAT Gateway"
        description={
          <>
            Delete NAT Gateway <strong>{deleteTarget}</strong>? This may disrupt traffic from
            private subnets.
          </>
        }
        isPending={deleteMut.isPending}
        onConfirm={() => deleteTarget && deleteMut.mutate(deleteTarget)}
      />
    </div>
  )
}

function CreateNatGatewayDialog({
  isPending,
  onClose,
  onSubmit,
}: {
  isPending: boolean
  onClose: () => void
  onSubmit: (params: { subnetId: string; allocationId: string }) => void
}) {
  const [subnetId, setSubnetId] = useState("")
  const [allocationId, setAllocationId] = useState("")

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create NAT Gateway</DialogTitle>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <p className="text-sm text-fg-muted">
            A NAT Gateway must be placed in a public subnet. You need an Elastic IP from the Elastic
            IPs tab.
          </p>
          <FormField label="Subnet ID">
            <Input
              placeholder="subnet-xxxxxxxx"
              value={subnetId}
              onChange={(e) => setSubnetId(e.target.value)}
            />
          </FormField>
          <FormField label="Elastic IP Allocation ID">
            <Input
              placeholder="eipalloc-xxxxxxxx"
              value={allocationId}
              onChange={(e) => setAllocationId(e.target.value)}
            />
          </FormField>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={!subnetId || !allocationId || isPending}
            onClick={() => onSubmit({ subnetId, allocationId })}
          >
            {isPending && <Spinner className="mr-2" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
