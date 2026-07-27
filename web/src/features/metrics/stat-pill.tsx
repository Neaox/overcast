import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
/** Small labeled value pill — shared by MetricsPage's static info row and HealthStrip. */
export function StatPill({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-md border border-border bg-bg-elevated px-3 py-2">
      <span className={cn(fieldLabel, "text-fg-muted")}>{label}</span>
      <span className="font-mono text-sm font-medium text-fg">{value}</span>
    </div>
  )
}
