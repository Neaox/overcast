import { Tooltip } from "@/components/ui/tooltip"

/** Label tooltip for the collapsed rail, where items show icons only. */
export function SidebarTooltip({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Tooltip content={label} side="right" delayDuration={100}>
      {children}
    </Tooltip>
  )
}
