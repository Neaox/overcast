import { useQuery } from "@tanstack/react-query"
import { ec2SubnetsQueryOptions, ec2SecurityGroupsQueryOptions } from "@/features/ec2/data"
import { Input } from "@/components/ui/input"
import type { AwsvpcNetworking } from "@/features/ecs/awsvpc"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

const selectClass = "w-full rounded border border-border bg-bg px-3 py-2 text-sm text-fg"

/** Reads the selected values out of a native multi-select. */
function selectedValues(el: HTMLSelectElement): string[] {
  return Array.from(el.selectedOptions, (o) => o.value)
}

/**
 * Subnet, security group and public-IP inputs for an awsvpc task.
 *
 * Subnets and security groups are offered from EC2 when the emulator has any.
 * When it has none, the field falls back to free text rather than blocking the
 * dialog — a task can reference a subnet that was never created through EC2,
 * and ECS itself does not require one to exist.
 */
export function AwsvpcFields({
  value,
  onChange,
  required,
}: {
  value: AwsvpcNetworking
  onChange: (next: AwsvpcNetworking) => void
  required: boolean
}) {
  const { data: subnets = [] } = useQuery(ec2SubnetsQueryOptions())
  const { data: securityGroups = [] } = useQuery(ec2SecurityGroupsQueryOptions())

  return (
    <>
      <div>
        <label className={cn(fieldLabel, "mb-1 block text-fg")}>
          Subnets{required && <span className="ml-1 text-danger">*</span>}
        </label>
        {subnets.length > 0 ? (
          <select
            multiple
            size={Math.min(subnets.length, 4)}
            value={value.subnets}
            onChange={(e) => onChange({ ...value, subnets: selectedValues(e.target) })}
            className={selectClass}
          >
            {subnets.map((s) => (
              <option key={s.subnetId} value={s.subnetId}>
                {s.subnetId} — {s.cidrBlock} ({s.availabilityZone})
              </option>
            ))}
          </select>
        ) : (
          <Input
            placeholder="subnet-12345678, subnet-87654321"
            value={value.subnets.join(", ")}
            onChange={(e) =>
              onChange({
                ...value,
                subnets: e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
          />
        )}
        {required && value.subnets.length === 0 && (
          <p className="mt-1 text-xs text-fg-muted">
            Fargate tasks use awsvpc networking and need at least one subnet.
          </p>
        )}
      </div>

      <div>
        <label className={cn(fieldLabel, "mb-1 block text-fg")}>Security Groups</label>
        {securityGroups.length > 0 ? (
          <select
            multiple
            size={Math.min(securityGroups.length, 4)}
            value={value.securityGroups}
            onChange={(e) => onChange({ ...value, securityGroups: selectedValues(e.target) })}
            className={selectClass}
          >
            {securityGroups.map((g) => (
              <option key={g.groupId} value={g.groupId}>
                {g.groupId} — {g.groupName}
              </option>
            ))}
          </select>
        ) : (
          <Input
            placeholder="sg-12345678"
            value={value.securityGroups.join(", ")}
            onChange={(e) =>
              onChange({
                ...value,
                securityGroups: e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
          />
        )}
      </div>

      <div>
        <label className={cn(fieldLabel, "mb-1 block text-fg")}>Assign Public IP</label>
        <select
          value={value.assignPublicIp}
          onChange={(e) => onChange({ ...value, assignPublicIp: e.target.value })}
          className={selectClass}
        >
          <option value="DISABLED">DISABLED</option>
          <option value="ENABLED">ENABLED</option>
        </select>
      </div>
    </>
  )
}
