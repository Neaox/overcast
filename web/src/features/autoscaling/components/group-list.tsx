import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { Activity, Boxes, SlidersHorizontal } from "lucide-react"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import {
  autoscalingActivitiesQueryOptions,
  autoscalingGroupsQueryOptions,
  autoscalingHooksQueryOptions,
  autoscalingKeys,
  autoscalingPoliciesQueryOptions,
  setDesiredCapacityMutationOptions,
} from "@/features/autoscaling/data"
import {
  activityTone,
  capacitySummary,
  formatTime,
  healthTone,
  isConverged,
  lifecycleTone,
} from "@/features/autoscaling/display"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { cn } from "@/lib/utils"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
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
import { QueryListState, Spinner, EmptyState } from "@/components/ui/primitives"
import {
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
  RowAction,
  RowActions,
} from "@/components/ui/resource-list-page"

export function AutoScalingGroupList() {
  const [selected, setSelected] = useState<string>()
  const [capacityTarget, setCapacityTarget] = useState<{ name: string; desired: number }>()
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const {
    data: groups = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(autoscalingGroupsQueryOptions())

  const capacityMut = useResourceMutation({
    options: setDesiredCapacityMutationOptions(),
    invalidateKeys: [autoscalingKeys.groups()],
    successTitle: "Desired capacity updated",
    successDescription: (vars) => `${vars.group} → ${vars.desiredCapacity}`,
    errorTitle: "Could not set desired capacity",
    onSuccess: () => setCapacityTarget(undefined),
  })

  const selectedGroup = groups.find((g) => g.AutoScalingGroupName === selected)

  return (
    <ResourceListPage
      title="Auto Scaling Groups"
      count={groups.length}
      actions={
        <>
          <ServiceDocsButton
            service="autoscaling"
            label="Auto Scaling"
            open={docsOpen}
            onOpen={openDocs}
            onClose={closeDocs}
          />
          <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
        </>
      }
    >
      <ResourceListCard>
        {isLoading || groups.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={groups.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Boxes className="h-10 w-10" />}
                title="No Auto Scaling groups"
                description="Create a launch configuration and a group — the reconciler will launch EC2 instances to match its desired capacity."
              />
            }
            errorTitle="Failed to load Auto Scaling groups"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Group</TableHead>
                <TableHead>In service / desired</TableHead>
                <TableHead>Min</TableHead>
                <TableHead>Max</TableHead>
                <TableHead>Launch configuration</TableHead>
                <TableHead>Availability zones</TableHead>
                <TableHead>Health check</TableHead>
                <TableHead className="w-20 text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {groups.map((group) => {
                const instances = group.Instances ?? []
                const inService = instances.filter(
                  (i) => i.LifecycleState === "InService",
                ).length
                const name = group.AutoScalingGroupName ?? ""
                return (
                  <TableRow
                    key={name}
                    className={cn(selected === name && "bg-bg-muted")}
                    onClick={() => setSelected(selected === name ? undefined : name)}
                  >
                    <TableCell>
                      <ResourceName icon={Boxes} name={name} />
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      <span className="inline-flex items-center gap-2">
                        {capacitySummary(inService, group.DesiredCapacity)}
                        {isConverged(instances, group.DesiredCapacity) ? null : (
                          <Badge variant="info">converging</Badge>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="text-fg-muted">{group.MinSize ?? 0}</TableCell>
                    <TableCell className="text-fg-muted">{group.MaxSize ?? 0}</TableCell>
                    <TableCell className="text-fg-muted">
                      {group.LaunchConfigurationName || "—"}
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {(group.AvailabilityZones ?? []).join(", ") || "—"}
                    </TableCell>
                    <TableCell className="text-fg-muted">{group.HealthCheckType || "—"}</TableCell>
                    <TableCell>
                      <RowActions>
                        <RowAction
                          label={`Set desired capacity for ${name}`}
                          onClick={() =>
                            setCapacityTarget({ name, desired: group.DesiredCapacity ?? 0 })
                          }
                        >
                          <SlidersHorizontal className="h-3.5 w-3.5" />
                        </RowAction>
                      </RowActions>
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </ResourceListCard>

      {selectedGroup ? (
        <GroupDetail
          name={selectedGroup.AutoScalingGroupName ?? ""}
          instances={selectedGroup.Instances ?? []}
        />
      ) : null}

      <SetCapacityDialog
        target={capacityTarget}
        onClose={() => setCapacityTarget(undefined)}
        isPending={capacityMut.isPending}
        onSubmit={(desiredCapacity) =>
          capacityTarget &&
          capacityMut.mutate({ group: capacityTarget.name, desiredCapacity })
        }
      />
    </ResourceListPage>
  )
}

// ─── Group detail ─────────────────────────────────────────────────────────

interface GroupInstance {
  InstanceId?: string
  InstanceType?: string
  AvailabilityZone?: string
  LifecycleState?: string
  HealthStatus?: string
  ProtectedFromScaleIn?: boolean
}

function GroupDetail({ name, instances }: { name: string; instances: GroupInstance[] }) {
  const { data: policies = [] } = useQuery(autoscalingPoliciesQueryOptions())
  const { data: hooks = [] } = useQuery(autoscalingHooksQueryOptions(name))
  const { data: activities = [] } = useQuery(autoscalingActivitiesQueryOptions(name))
  const groupPolicies = policies.filter((p) => p.AutoScalingGroupName === name)

  return (
    <div className="mt-4 grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Instances</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          {instances.length === 0 ? (
            <p className="text-fg-muted text-sm">No instances yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Instance</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Zone</TableHead>
                  <TableHead>Lifecycle</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead>Protected</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {instances.map((instance) => (
                  <TableRow key={instance.InstanceId}>
                    <TableCell className="font-mono text-xs">{instance.InstanceId}</TableCell>
                    <TableCell className="text-fg-muted">{instance.InstanceType || "—"}</TableCell>
                    <TableCell className="text-fg-muted">
                      {instance.AvailabilityZone || "—"}
                    </TableCell>
                    <TableCell>
                      <Badge variant={lifecycleTone(instance.LifecycleState)}>
                        {instance.LifecycleState ?? "unknown"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={healthTone(instance.HealthStatus)}>
                        {instance.HealthStatus ?? "unknown"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {instance.ProtectedFromScaleIn ? "Yes" : "No"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Scaling policies</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          {groupPolicies.length === 0 ? (
            <p className="text-fg-muted text-sm">No scaling policies.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Policy</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Adjustment</TableHead>
                  <TableHead>Cooldown</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {groupPolicies.map((policy) => (
                  <TableRow key={policy.PolicyARN}>
                    <TableCell>{policy.PolicyName}</TableCell>
                    <TableCell className="text-fg-muted">{policy.PolicyType || "—"}</TableCell>
                    <TableCell className="text-fg-muted">
                      {policy.PolicyType === "StepScaling"
                        ? `${(policy.StepAdjustments ?? []).length} step(s)`
                        : `${policy.AdjustmentType ?? "—"} ${policy.ScalingAdjustment ?? 0}`}
                    </TableCell>
                    <TableCell className="text-fg-muted">
                      {policy.Cooldown != null ? `${policy.Cooldown}s` : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Lifecycle hooks</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          {hooks.length === 0 ? (
            <p className="text-fg-muted text-sm">No lifecycle hooks.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Hook</TableHead>
                  <TableHead>Transition</TableHead>
                  <TableHead>Default result</TableHead>
                  <TableHead>Heartbeat</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {hooks.map((hook) => (
                  <TableRow key={hook.LifecycleHookName}>
                    <TableCell>{hook.LifecycleHookName}</TableCell>
                    <TableCell className="text-fg-muted">
                      {(hook.LifecycleTransition ?? "").replace("autoscaling:", "")}
                    </TableCell>
                    <TableCell className="text-fg-muted">{hook.DefaultResult || "—"}</TableCell>
                    <TableCell className="text-fg-muted">
                      {hook.HeartbeatTimeout != null ? `${hook.HeartbeatTimeout}s` : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent scaling activities</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          {activities.length === 0 ? (
            <p className="text-fg-muted flex items-center gap-2 text-sm">
              <Activity className="h-4 w-4" /> Nothing has scaled yet.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Started</TableHead>
                  <TableHead>Description</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {activities.slice(0, 15).map((activity) => (
                  <TableRow key={activity.ActivityId}>
                    <TableCell className="text-fg-muted whitespace-nowrap">
                      {formatTime(activity.StartTime)}
                    </TableCell>
                    <TableCell>{activity.Description}</TableCell>
                    <TableCell>
                      <Badge variant={activityTone(activity.StatusCode)}>
                        {activity.StatusCode ?? "unknown"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// ─── Set desired capacity ─────────────────────────────────────────────────

const capacitySchema = z.object({
  DesiredCapacity: z.number().int().min(0, "Must be zero or more"),
})

function SetCapacityDialog({
  target,
  onClose,
  isPending,
  onSubmit,
}: {
  target?: { name: string; desired: number }
  onClose: () => void
  isPending: boolean
  onSubmit: (desiredCapacity: number) => void
}) {
  const form = useForm({
    validators: { onChange: capacitySchema },
    defaultValues: { DesiredCapacity: target?.desired ?? 0 },
    onSubmit: ({ value }) => onSubmit(Number(value.DesiredCapacity)),
  })

  return (
    <Dialog
      open={!!target}
      onOpenChange={(v) => {
        if (!v) {
          onClose()
          form.reset()
        }
      }}
    >
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>Set desired capacity</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <DialogBody className="space-y-4">
            <p className="text-fg-muted text-sm">
              The reconciler will launch or terminate EC2 instances for{" "}
              <strong>{target?.name}</strong> until the group matches. A value outside the
              group&rsquo;s min/max is rejected, as it is on AWS.
            </p>
            <form.Field name="DesiredCapacity">
              {(field) => (
                <FormField
                  label="Desired capacity"
                  error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
                >
                  <Input
                    type="number"
                    min={0}
                    value={String(field.state.value)}
                    onChange={(e) => field.handleChange(Number(e.target.value))}
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
              Apply
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
