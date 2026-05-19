import type { LambdaFunction } from "@/types"
import { Badge } from "@/components/ui/badge"
import { Link } from "@tanstack/react-router"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function FunctionOverview({ fn }: { fn: LambdaFunction }) {
  const logGroup = fn.LoggingConfig?.LogGroup
  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm sm:grid-cols-3 lg:grid-cols-5">
        <OverviewItem label="Function ARN" value={<ArnText arn={fn.FunctionArn ?? ""} />} mono />
        <OverviewItem label="Runtime" value={fn.Runtime || "—"} />
        <OverviewItem label="Handler" value={fn.Handler || "—"} mono />
        <OverviewItem label="Package type" value={fn.PackageType ?? "Zip"} />
        <OverviewItem
          label="State"
          value={<Badge variant={fn.State === "Active" ? "success" : "default"}>{fn.State}</Badge>}
        />
        <OverviewItem label="Memory" value={`${fn.MemorySize ?? 128} MB`} />
        <OverviewItem label="Timeout" value={`${fn.Timeout ?? 3}s`} />
        <OverviewItem label="Code size" value={fn.CodeSize ? `${fn.CodeSize} bytes` : "—"} />
        <OverviewItem label="Last modified" value={fn.LastModified || "—"} />
        <OverviewItem label="Architectures" value={(fn.Architectures ?? []).join(", ")} />
        <OverviewItem
          label="Log group"
          value={
            logGroup ? (
              <Link
                to="/cloudwatch/logs/group"
                search={{ groupName: logGroup }}
                className="font-mono text-xs text-accent hover:underline"
              >
                {logGroup}
              </Link>
            ) : (
              "—"
            )
          }
          mono
        />
      </div>
    </div>
  )
}

export function OverviewItem({
  label,
  value,
  mono = false,
}: {
  label: string
  value: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-fg-muted">{label}</span>
      <span className={cn("break-all text-fg", mono && "font-mono text-xs")}>{value}</span>
    </div>
  )
}
