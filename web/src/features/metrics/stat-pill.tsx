/** Small labeled value pill — shared by MetricsPage's static info row and HealthStrip. */
export function StatPill({ label, value }: { label: string; value: string }) {
  return (
    <div className="bg-bg-card flex flex-col gap-0.5 rounded-md border border-border px-3 py-2">
      <span className="text-[10px] font-medium tracking-wider text-fg-muted uppercase">
        {label}
      </span>
      <span className="font-mono text-sm font-medium text-fg">{value}</span>
    </div>
  )
}
