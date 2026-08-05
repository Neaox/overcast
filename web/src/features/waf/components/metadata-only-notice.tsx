import { AlertTriangle } from "lucide-react"

export function MetadataOnlyNotice() {
  return (
    <div className="flex gap-3 rounded-lg border border-amber-400/30 bg-amber-400/10 p-4 text-sm text-amber-100">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <p className="font-semibold">Metadata only — Web ACL rules are not enforced</p>
        <p className="mt-1 text-amber-100/80">
          Overcast stores Web ACL configuration for SDK and CloudFormation workflows, but it does
          not inspect or block application traffic. Unsupported WAFv2 operations and WAF Classic
          return 501.
        </p>
      </div>
    </div>
  )
}
