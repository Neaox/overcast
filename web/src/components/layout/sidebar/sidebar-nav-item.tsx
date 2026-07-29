import { Link } from "@tanstack/react-router"
import { ChevronDown, GripVertical, type LucideIcon } from "lucide-react"
import type { DraggableAttributes, DraggableSyntheticListeners } from "@dnd-kit/core"
import { cva } from "class-variance-authority"
import { cn } from "@/lib/utils"
import type { SubNavChild } from "@/lib/nav-services"
import { flatChildren } from "./nav-children"
import { SidebarBadge } from "./sidebar-badge"
import { SidebarSubNav } from "./sidebar-sub-nav"
import { SidebarTooltip } from "./sidebar-tooltip"

export interface SidebarNavEntry {
  to: string
  label: string
  icon: LucideIcon
  children?: SubNavChild[]
}

/** Wiring handed down by the sortable wrapper — all applied to the row element. */
export interface SidebarSortableProps {
  ref: (element: HTMLDivElement | null) => void
  style: React.CSSProperties
  attributes: DraggableAttributes
  listeners: DraggableSyntheticListeners
}

const rowVariants = cva(
  "group relative flex items-center rounded-control font-mono transition-colors",
  {
    variants: {
      collapsed: {
        true: "h-8 w-9 justify-center",
        // Padding lives on the interactive child instead — see `rowHitArea`.
        false: "text-[13px]",
      },
      active: { true: "bg-accent-muted text-accent", false: "" },
      tone: { default: "text-fg-muted", muted: "text-fg-subtle" },
    },
    compoundVariants: [
      { active: false, class: "hover:bg-sidebar-item-hover hover:text-accent" },
      { active: true, collapsed: false, class: "font-bold" },
      { active: true, tone: "default", class: "text-accent" },
      { active: true, tone: "muted", class: "text-accent" },
    ],
    defaultVariants: { collapsed: false, active: false, tone: "default" },
  },
)

/**
 * The link or button inside an expanded row. It carries the row padding and
 * stretches to fill the row so the clickable area matches the hovered area.
 */
const rowHitArea = "flex min-w-0 flex-1 items-center gap-2.5 px-2.5 py-[7px]"

interface SidebarNavItemProps {
  item: SidebarNavEntry
  collapsed: boolean
  pathname: string
  tone?: "default" | "muted"
  expanded?: boolean
  onToggleExpand?: (key: string) => void
  badge?: { count: number; label: string }
  sortable?: SidebarSortableProps
}

export function SidebarNavItem({
  item,
  collapsed,
  pathname,
  tone = "default",
  expanded = false,
  onToggleExpand,
  badge,
  sortable,
}: SidebarNavItemProps) {
  const { to, label, icon: Icon, children } = item
  const active = to === "/" ? pathname === "/" : pathname.startsWith(to)
  const rowCls = rowVariants({ collapsed, active, tone })
  const iconCls = collapsed ? "h-[17px] w-[17px] shrink-0" : "h-4 w-4 shrink-0"
  const badgeNode = badge && badge.count > 0 && (
    <SidebarBadge count={badge.count} collapsed={collapsed} label={badge.label} />
  )

  if (collapsed) {
    const target = children?.length ? flatChildren(children)[0].to : to
    return (
      <SidebarTooltip label={label}>
        <Link to={target} className={rowCls} aria-label={label}>
          <Icon className={iconCls} />
          {badgeNode}
        </Link>
      </SidebarTooltip>
    )
  }

  if (children?.length) {
    return (
      <div>
        <div
          ref={sortable?.ref}
          style={sortable?.style}
          className={rowCls}
          {...sortable?.attributes}
          {...sortable?.listeners}
        >
          {sortable && <SidebarGrip />}
          <button
            onClick={(event) => {
              event.stopPropagation()
              onToggleExpand?.(to)
            }}
            className={cn(rowHitArea, "text-left")}
            aria-expanded={expanded}
          >
            <Icon className={iconCls} />
            <span className="flex-1 truncate">{label}</span>
            <ChevronDown
              className={cn("h-3 w-3 shrink-0 transition-transform", expanded && "rotate-180")}
            />
          </button>
        </div>
        {expanded && <SidebarSubNav items={children} pathname={pathname} />}
      </div>
    )
  }

  return (
    <div
      ref={sortable?.ref}
      style={sortable?.style}
      className={rowCls}
      {...sortable?.attributes}
      {...sortable?.listeners}
    >
      {sortable && <SidebarGrip />}
      <Link
        to={to}
        className={rowHitArea}
        draggable={false}
      >
        <Icon className={iconCls} />
        <span className="truncate">{label}</span>
        {badgeNode}
      </Link>
    </div>
  )
}

/**
 * Drag affordance — absolutely positioned so it never shifts icon alignment, and
 * click-through so it does not punch a hole in the link it overlaps. It hangs into the
 * nav's left gutter (`px-2` on the `<nav>`) so it clears the service icon rather than
 * sitting on top of it.
 */
function SidebarGrip() {
  return (
    <GripVertical className="pointer-events-none absolute top-1/2 -left-1 h-3 w-3 -translate-y-1/2 text-fg-subtle/40 opacity-0 transition-opacity group-hover:opacity-100" />
  )
}
