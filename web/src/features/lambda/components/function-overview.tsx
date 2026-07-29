import type { LambdaFunction } from "@/types"
import { Badge } from "@/components/ui/badge"
import { Link } from "@tanstack/react-router"
import { Definition, DefinitionList } from "@/components/ui/definition-card"
import { ArnText } from "@/components/ui/arn-link"

export function FunctionOverview({ fn }: { fn: LambdaFunction }) {
  const logGroup = fn.LoggingConfig?.LogGroup
  return (
    <div className="rounded-lg border border-border bg-bg-elevated p-4">
      {/* Wide and shallow: eleven short fields, so this one keeps a pinned
          count rather than the container default's two or three. */}
      <DefinitionList columns={4} className="gap-y-2">
        <Definition label="Runtime" value={fn.Runtime} />
        <Definition label="Handler" value={fn.Handler} />
        <Definition label="Package type" value={fn.PackageType ?? "Zip"} />
        <Definition
          label="State"
          value={<Badge variant={fn.State === "Active" ? "success" : "default"}>{fn.State}</Badge>}
        />
        <Definition label="Memory" value={`${fn.MemorySize ?? 128} MB`} />
        <Definition label="Timeout" value={`${fn.Timeout ?? 3}s`} />
        <Definition label="Code size" value={fn.CodeSize ? `${fn.CodeSize} bytes` : null} />
        <Definition label="Last modified" value={fn.LastModified} />
        <Definition label="Architectures" value={(fn.Architectures ?? []).join(", ")} />
        <Definition
          label="Log group"
          value={
            logGroup ? (
              <Link
                to="/cloudwatch/logs/group"
                search={{ groupName: logGroup }}
                className="text-accent hover:underline"
              >
                {logGroup}
              </Link>
            ) : null
          }
          full
        />
        <Definition
          label="Function ARN"
          value={fn.FunctionArn ? <ArnText arn={fn.FunctionArn} /> : null}
          full
        />
      </DefinitionList>
    </div>
  )
}
