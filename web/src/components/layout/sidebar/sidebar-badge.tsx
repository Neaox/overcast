import { cn } from "@/lib/utils"

interface SidebarBadgeProps {
  count: number
  collapsed: boolean
  label: string
}

/** Unread count — a pill in the expanded sidebar, a dot in the collapsed rail. */
export function SidebarBadge({ count, collapsed, label }: SidebarBadgeProps) {
  return (
    <span
      aria-label={label}
      className={cn(
        "rounded-full bg-accent text-fg-on-accent",
        collapsed
          ? "absolute top-[3px] right-[3px] h-1.5 w-1.5"
          : "ml-auto px-1.5 py-px font-mono text-2xs leading-4 font-normal tabular-nums",
      )}
    >
      {!collapsed && (count > 99 ? "99+" : count)}
    </span>
  )
}
