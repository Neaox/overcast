import { AlertTriangle } from "lucide-react"

/**
 * Colours come from the semantic `warning` tokens rather than Tailwind's amber
 * scale. A fixed shade can only suit one theme: `text-amber-100` is a near-white
 * amber, which reads on the dark surface this notice was written against and
 * disappears against the light one.
 */
export function MetadataOnlyNotice() {
  return (
    <div className="flex gap-3 rounded-lg border border-warning/40 bg-warning/10 p-4 text-sm text-warning">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <p className="font-semibold">Metadata only — Web ACL rules are not enforced</p>
        <p className="mt-1 text-fg-muted">
          Overcast stores Web ACL configuration for SDK and CloudFormation workflows, but it does
          not inspect or block application traffic. Unsupported WAFv2 operations and WAF Classic
          return 501.
        </p>
      </div>
    </div>
  )
}
