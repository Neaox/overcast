import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { Activity, Boxes, ChevronRight, SlidersHorizontal } from "lucide-react"
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
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"
import {
  RefreshAction,
  ResourceListPage,
  ResourceName,
  RowAction,
} from "@/components/ui/resource-list-page"
import { ResourceTable, type ResourceTableSort } from "@/components/ui/resource-table"

interface AutoScalingGroupListProps {
  /** Current table sort — owned by the route's `sort` search param, see `useSortSearchParam`. */
  sort?: ResourceTableSort
  onSortChange?: (next: ResourceTableSort | undefined) => void
}

export function AutoScalingGroupList({ sort, onSortChange }: AutoScalingGroupListProps = {}) {
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
      {/*
       * The expanded group's detail renders below the table rather than as a
       * tinted row: `ResourceTable` owns row rendering and has no per-row
       * class hook, so the chevron column carries the expansion state — the
       * shape `apigateway/usage-plans-page` already established.
       */}
      <ResourceTable
        query={{ data: groups, isLoading, error }}
        noun="Auto Scaling groups"
        emptyIcon={Boxes}
        emptyTitle="No Auto Scaling groups"
        emptyDescription="Create a launch configuration and a group — the reconciler will launch EC2 instances to match its desired capacity."
        errorTitle="Failed to load Auto Scaling groups"
        sort={sort}
        onSortChange={onSortChange}
        rowKey={(group) => group.AutoScalingGroupName ?? ""}
        onRowClick={(group) =>
          setSelected(
            selected === group.AutoScalingGroupName ? undefined : group.AutoScalingGroupName,
          )
        }
        columns={[
          {
            header: "",
            headerClassName: "w-8",
            cellClassName: "w-8",
            hideable: false,
            cell: (group) => (
              <ChevronRight
                className={cn(
                  "h-4 w-4 text-fg-muted transition-transform",
                  selected === group.AutoScalingGroupName && "rotate-90",
                )}
              />
            ),
          },
          {
            // The id is the `?sort=` token; pinning it survives a reworded header.
            id: "group",
            header: "Group",
            // The identity column stays put — the chevron column ahead of it is
            // chrome, so "Group" is what a reader scans for.
            hideable: false,
            sortValue: (group) => group.AutoScalingGroupName,
            cell: (group) => <ResourceName icon={Boxes} name={group.AutoScalingGroupName ?? ""} />,
          },
          {
            header: "In service / desired",
            cellClassName: "font-mono text-xs",
            sortValue: (group) => inServiceCount(group.Instances ?? []),
            cell: (group) => (
              <span className="inline-flex items-center gap-2">
                {capacitySummary(inServiceCount(group.Instances ?? []), group.DesiredCapacity)}
                {isConverged(group.Instances ?? [], group.DesiredCapacity) ? null : (
                  <Badge variant="info">converging</Badge>
                )}
              </span>
            ),
          },
          {
            header: "Min",
            cellClassName: "text-fg-muted",
            sortValue: (group) => group.MinSize ?? 0,
            cell: (group) => group.MinSize ?? 0,
          },
          {
            header: "Max",
            cellClassName: "text-fg-muted",
            sortValue: (group) => group.MaxSize ?? 0,
            cell: (group) => group.MaxSize ?? 0,
          },
          {
            header: "Launch configuration",
            cellClassName: "text-fg-muted",
            cell: (group) => group.LaunchConfigurationName || "—",
          },
          {
            header: "Availability zones",
            cellClassName: "text-fg-muted",
            cell: (group) => (group.AvailabilityZones ?? []).join(", ") || "—",
          },
          {
            header: "Health check",
            cellClassName: "text-fg-muted",
            cell: (group) => group.HealthCheckType || "—",
          },
        ]}
        rowActions={(group) => (
          <RowAction
            label={`Set desired capacity for ${group.AutoScalingGroupName ?? ""}`}
            onClick={() =>
              setCapacityTarget({
                name: group.AutoScalingGroupName ?? "",
                desired: group.DesiredCapacity ?? 0,
              })
            }
          >
            <SlidersHorizontal className="h-3.5 w-3.5" />
          </RowAction>
        )}
      />

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
          capacityTarget && capacityMut.mutate({ group: capacityTarget.name, desiredCapacity })
        }
      />
    </ResourceListPage>
  )
}

// ─── Group detail ─────────────────────────────────────────────────────────

/** Numerator of "in service / desired" — also what the column sorts by. */
function inServiceCount(instances: Array<{ LifecycleState?: string }>): number {
  return instances.filter((i) => i.LifecycleState === "InService").length
}

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
          <ResourceTable
            variant="embedded"
            query={{ data: instances, isLoading: false }}
            noun="instances"
            emptyTitle="No instances yet."
            rowKey={(instance) => instance.InstanceId ?? ""}
            columns={[
              {
                header: "Instance",
                cellClassName: "font-mono text-xs",
                sortValue: (instance) => instance.InstanceId,
                cell: (instance) => instance.InstanceId,
              },
              {
                header: "Type",
                cellClassName: "text-fg-muted",
                cell: (instance) => instance.InstanceType || "—",
              },
              {
                header: "Zone",
                cellClassName: "text-fg-muted",
                cell: (instance) => instance.AvailabilityZone || "—",
              },
              {
                header: "Lifecycle",
                cell: (instance) => (
                  <Badge variant={lifecycleTone(instance.LifecycleState)}>
                    {instance.LifecycleState ?? "unknown"}
                  </Badge>
                ),
              },
              {
                header: "Health",
                cell: (instance) => (
                  <Badge variant={healthTone(instance.HealthStatus)}>
                    {instance.HealthStatus ?? "unknown"}
                  </Badge>
                ),
              },
              {
                header: "Protected",
                cellClassName: "text-fg-muted",
                cell: (instance) => (instance.ProtectedFromScaleIn ? "Yes" : "No"),
              },
            ]}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Scaling policies</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          <ResourceTable
            variant="embedded"
            query={{ data: groupPolicies, isLoading: false }}
            noun="scaling policies"
            emptyTitle="No scaling policies."
            rowKey={(policy) => policy.PolicyARN ?? policy.PolicyName ?? ""}
            columns={[
              {
                header: "Policy",
                sortValue: (policy) => policy.PolicyName,
                cell: (policy) => policy.PolicyName,
              },
              {
                header: "Type",
                cellClassName: "text-fg-muted",
                cell: (policy) => policy.PolicyType || "—",
              },
              {
                header: "Adjustment",
                cellClassName: "text-fg-muted",
                cell: (policy) =>
                  policy.PolicyType === "StepScaling"
                    ? `${(policy.StepAdjustments ?? []).length} step(s)`
                    : `${policy.AdjustmentType ?? "—"} ${policy.ScalingAdjustment ?? 0}`,
              },
              {
                header: "Cooldown",
                cellClassName: "text-fg-muted",
                cell: (policy) => (policy.Cooldown != null ? `${policy.Cooldown}s` : "—"),
              },
            ]}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Lifecycle hooks</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          <ResourceTable
            variant="embedded"
            query={{ data: hooks, isLoading: false }}
            noun="lifecycle hooks"
            emptyTitle="No lifecycle hooks."
            rowKey={(hook) => hook.LifecycleHookName ?? ""}
            columns={[
              {
                header: "Hook",
                sortValue: (hook) => hook.LifecycleHookName,
                cell: (hook) => hook.LifecycleHookName,
              },
              {
                header: "Transition",
                cellClassName: "text-fg-muted",
                cell: (hook) => (hook.LifecycleTransition ?? "").replace("autoscaling:", ""),
              },
              {
                header: "Default result",
                cellClassName: "text-fg-muted",
                cell: (hook) => hook.DefaultResult || "—",
              },
              {
                header: "Heartbeat",
                cellClassName: "text-fg-muted",
                cell: (hook) => (hook.HeartbeatTimeout != null ? `${hook.HeartbeatTimeout}s` : "—"),
              },
            ]}
          />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Recent scaling activities</CardTitle>
        </CardHeader>
        <CardContent className="overflow-x-auto">
          <ResourceTable
            variant="embedded"
            // Still the 15 most recent the panel always showed; the sort orders
            // that window rather than reaching past it.
            query={{ data: activities.slice(0, 15), isLoading: false }}
            noun="scaling activities"
            emptyIcon={Activity}
            emptyTitle="Nothing has scaled yet."
            rowKey={(activity) => activity.ActivityId ?? ""}
            defaultSort={{ id: "started", desc: true }}
            columns={[
              {
                id: "started",
                header: "Started",
                cellClassName: "text-fg-muted whitespace-nowrap",
                sortValue: (activity) => activity.StartTime,
                cell: (activity) => formatTime(activity.StartTime),
              },
              { header: "Description", cell: (activity) => activity.Description },
              {
                header: "Status",
                cell: (activity) => (
                  <Badge variant={activityTone(activity.StatusCode)}>
                    {activity.StatusCode ?? "unknown"}
                  </Badge>
                ),
              },
            ]}
          />
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
            <p className="text-sm text-fg-muted">
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
