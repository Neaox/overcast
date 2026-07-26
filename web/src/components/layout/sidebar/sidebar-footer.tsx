import { ChevronLeft, ChevronRight } from "lucide-react"
import { cn } from "@/lib/utils"
import { BOTTOM_ITEMS } from "@/lib/nav-services"
import { useDebugEnabled } from "@/hooks/use-server-info"
import { SidebarNavItem } from "./sidebar-nav-item"
import { SidebarTooltip } from "./sidebar-tooltip"

interface SidebarFooterProps {
  collapsed: boolean
  pathname: string
  unreadCount: number
  onToggleCollapsed: () => void
}

/** Tool links plus the collapse toggle, pinned to the bottom of the sidebar. */
export function SidebarFooter({
  collapsed,
  pathname,
  unreadCount,
  onToggleCollapsed,
}: SidebarFooterProps) {
  const debugEnabled = useDebugEnabled()
  const items = BOTTOM_ITEMS.filter((item) => !item.debugOnly || debugEnabled)
  const toggleLabel = collapsed ? "Expand sidebar" : "Collapse sidebar"

  const toggle = (
    <button
      onClick={onToggleCollapsed}
      aria-label={collapsed ? toggleLabel : undefined}
      className={cn(
        "mt-1 flex items-center rounded-control font-mono text-xs text-fg-subtle transition-colors",
        "hover:bg-sidebar-item-hover hover:text-accent",
        collapsed ? "h-8 w-9 justify-center" : "gap-2.5 px-2.5 py-[7px]",
      )}
    >
      {collapsed ? (
        <ChevronRight className="h-[17px] w-[17px]" />
      ) : (
        <>
          <ChevronLeft className="h-4 w-4" />
          <span>Collapse</span>
        </>
      )}
    </button>
  )

  return (
    <div
      className={cn(
        "mt-auto flex shrink-0 flex-col border-t border-border p-2",
        collapsed ? "w-full items-center gap-0.5" : "gap-px",
      )}
    >
      {items.map((item) => (
        <SidebarNavItem
          key={item.key}
          item={item}
          collapsed={collapsed}
          pathname={pathname}
          tone="muted"
          badge={
            item.to === "/inbox"
              ? {
                  count: unreadCount,
                  label: `${unreadCount} unread inbox message${unreadCount === 1 ? "" : "s"}`,
                }
              : undefined
          }
        />
      ))}
      {collapsed ? <SidebarTooltip label={toggleLabel}>{toggle}</SidebarTooltip> : toggle}
    </div>
  )
}
