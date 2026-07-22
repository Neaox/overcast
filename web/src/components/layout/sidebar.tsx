import { useState, useCallback } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link, useRouterState } from "@tanstack/react-router"
import {
  ChevronLeft,
  ChevronRight,
  ChevronDown,
  Cloud,
  GripVertical,
  type LucideIcon,
} from "lucide-react"
import {
  DndContext,
  closestCenter,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DraggableAttributes,
  type DraggableSyntheticListeners,
} from "@dnd-kit/core"
import {
  SortableContext,
  verticalListSortingStrategy,
  useSortable,
  arrayMove,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { cn } from "@/lib/utils"
import { useFavourites } from "@/hooks/use-favourites"
import { useDebugEnabled } from "@/hooks/use-server-info"
import { inboxMessagesQueryOptions } from "@/features/mail/data"
import { useInboxReadState } from "@/features/mail/read-state"
import {
  ALL_SERVICES,
  DASHBOARD_ITEM,
  BOTTOM_ITEMS,
  type SubNavChild,
  type SubNavGroup,
} from "@/lib/nav-services"

const SERVICE_BY_KEY = Object.fromEntries(ALL_SERVICES.map((s) => [s.key, s])) as Record<
  string,
  (typeof ALL_SERVICES)[number] | undefined
>

function isGroup(c: SubNavChild): c is SubNavGroup {
  return "group" in c
}

function flatChildren(children: SubNavChild[]): { to: string; label: string }[] {
  return children.flatMap((c) => (isGroup(c) ? c.items : [c]))
}

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  color: string
  children?: SubNavChild[]
}

interface SortableNavItemProps {
  item: NavItem
  collapsed: boolean
  pathname: string
  effectiveExpanded: Record<string, boolean | undefined>
  toggleExpand: (key: string) => void
  isDragging?: boolean
}

function SortableNavItem({
  item,
  collapsed,
  pathname,
  effectiveExpanded,
  toggleExpand,
  isDragging,
}: SortableNavItemProps) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({
    id: item.to,
  })

  return (
    <NavItemContent
      item={item}
      collapsed={collapsed}
      pathname={pathname}
      effectiveExpanded={effectiveExpanded}
      toggleExpand={toggleExpand}
      sortableRef={setNodeRef}
      sortableStyle={{
        transform: CSS.Transform.toString(transform),
        transition,
        opacity: isDragging ? 0.4 : 1,
      }}
      sortableAttributes={attributes}
      sortableListeners={listeners}
    />
  )
}

interface NavItemContentProps {
  item: NavItem
  collapsed: boolean
  pathname: string
  effectiveExpanded: Record<string, boolean | undefined>
  toggleExpand: (key: string) => void
  // Sortable props — all applied to the row div so setNodeRef and listeners share one element
  sortableRef?: (el: HTMLDivElement | null) => void
  sortableStyle?: React.CSSProperties
  sortableAttributes?: DraggableAttributes
  sortableListeners?: DraggableSyntheticListeners
}

function NavItemContent({
  item,
  collapsed,
  pathname,
  effectiveExpanded,
  toggleExpand,
  sortableRef,
  sortableStyle,
  sortableAttributes,
  sortableListeners,
}: NavItemContentProps) {
  const { to, label, icon: Icon, color, children } = item
  const active = to === "/" ? pathname === "/" : pathname.startsWith(to)
  const isOpen = effectiveExpanded[to] ?? false

  const iconCls = cn("h-4 w-4 shrink-0", active ? "text-fg-on-accent" : color)

  // Collapsed variants — just the icon centred, no grip
  if (collapsed) {
    const cls = cn(
      "mx-1 my-0.5 flex h-8 items-center justify-center rounded-md text-sm font-medium transition-colors",
      active
        ? "bg-sidebar-item-active text-sidebar-item-active-fg"
        : "text-sidebar-fg hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
    )
    if (children && children.length > 0) {
      const firstChild = flatChildren(children)[0]
      return (
        <Link to={firstChild.to} className={cls} title={label}>
          <Icon className={iconCls} />
        </Link>
      )
    }
    return (
      <Link to={to} className={cls} title={label}>
        <Icon className={iconCls} />
      </Link>
    )
  }

  // Shared row class — always px-2 gap-2.5 for both leaf and expandable so icons align
  const rowCls = cn(
    "group relative mx-2 my-0.5 flex h-8 items-center gap-2.5 rounded-md px-2 text-sm font-medium transition-colors",
    active
      ? "bg-sidebar-item-active text-sidebar-item-active-fg"
      : "text-sidebar-fg hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
  )

  // Grip icon — absolutely positioned so it doesn't affect layout/alignment
  const gripIcon = sortableListeners && (
    <GripVertical className="absolute top-1/2 left-0.5 h-3 w-3 -translate-y-1/2 text-sidebar-fg/25 opacity-0 transition-opacity group-hover:opacity-100" />
  )

  if (children && children.length > 0) {
    return (
      <div>
        {/* Row div is the sortable element — ref, style, attributes, listeners all here */}
        <div
          ref={sortableRef}
          style={sortableStyle}
          className={rowCls}
          {...sortableAttributes}
          {...sortableListeners}
        >
          {gripIcon}
          <button
            onClick={(e) => {
              e.stopPropagation()
              toggleExpand(to)
            }}
            className="flex flex-1 items-center gap-2.5 text-left"
          >
            <Icon className={iconCls} />
            <span className="flex-1">{label}</span>
            <ChevronDown
              className={cn("h-3 w-3 shrink-0 transition-transform", isOpen && "rotate-180")}
            />
          </button>
        </div>
        {isOpen && (
          <div className="ml-4 border-l border-sidebar-fg/10 pl-2">
            {(() => {
              const flat = flatChildren(children)
              const bestMatch = flat
                .filter((s) => pathname.startsWith(s.to))
                .sort((a, b) => b.to.length - a.to.length)[0] as
                | { to: string; label: string }
                | undefined

              function renderItem(child: { to: string; label: string }) {
                const childActive = bestMatch?.to === child.to
                return (
                  <Link
                    key={child.to}
                    to={child.to}
                    className={cn(
                      "my-0.5 flex h-7 items-center rounded-md px-2 text-sm transition-colors",
                      childActive
                        ? "font-medium text-accent"
                        : "text-sidebar-fg hover:text-sidebar-fg-strong",
                    )}
                  >
                    {child.label}
                  </Link>
                )
              }

              return children.map((child, i) => {
                if (isGroup(child)) {
                  return (
                    <div key={child.group}>
                      <p
                        className={cn(
                          "px-2 text-[10px] font-semibold tracking-wider text-sidebar-fg/40 uppercase pb-0.5",
                          i > 0 && "mt-2",
                        )}
                      >
                        {child.group}
                      </p>
                      {child.items.map(renderItem)}
                    </div>
                  )
                }
                return renderItem(child)
              })
            })()}
          </div>
        )}
      </div>
    )
  }

  return (
    // Row div is the sortable element — ref, style, attributes, listeners all here
    <div
      ref={sortableRef}
      style={sortableStyle}
      className={rowCls}
      {...sortableAttributes}
      {...sortableListeners}
    >
      {gripIcon}
      <Link
        to={to}
        className="flex flex-1 items-center gap-2.5"
        // Prevent the Link from swallowing pointer events before dnd-kit sees them
        draggable={false}
      >
        <Icon className={iconCls} />
        <span>{label}</span>
      </Link>
    </div>
  )
}

export function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean | undefined>>({})
  const [activeId, setActiveId] = useState<string | null>(null)
  const { favourites, reorderFavourites } = useFavourites()
  const router = useRouterState()
  const pathname = router.location.pathname
  const { data: inboxMessages = [] } = useQuery(inboxMessagesQueryOptions())
  const { unreadCount } = useInboxReadState(inboxMessages)
  const debugEnabled = useDebugEnabled()
  const bottomItems = BOTTOM_ITEMS.filter((item) => !item.debugOnly || debugEnabled)

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))

  const toggleExpand = useCallback((key: string) => {
    setExpanded((prev) => ({ ...prev, [key]: !prev[key] }))
  }, [])

  // Auto-expand parent items when a child route is active
  const effectiveExpanded = { ...expanded }
  for (const svc of ALL_SERVICES) {
    if (svc.children && effectiveExpanded[svc.to] === undefined) {
      const flat = flatChildren(svc.children)
      if (flat.some((c) => pathname.startsWith(c.to))) {
        effectiveExpanded[svc.to] = true
      }
    }
  }

  function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event
    setActiveId(null)
    if (over && active.id !== over.id) {
      const oldIndex = favourites.indexOf(active.id as string)
      const newIndex = favourites.indexOf(over.id as string)
      reorderFavourites(arrayMove(favourites, oldIndex, newIndex))
    }
  }

  // Current active service that isn't already pinned as a favourite.
  // Pick the longest matching `to` prefix so nested routes resolve correctly.
  const currentService =
    pathname === "/"
      ? undefined
      : ALL_SERVICES.filter(
          (svc) => pathname.startsWith(svc.to) && !favourites.includes(svc.key),
        ).sort((a, b) => b.to.length - a.to.length)[0]

  return (
    <aside
      className={cn(
        "flex shrink-0 flex-col border-r border-sidebar-bg/50 bg-sidebar-bg transition-all duration-200",
        collapsed ? "w-14" : "w-52",
      )}
    >
      {/* Logo */}
      <div
        className={cn(
          "flex h-12 shrink-0 items-center gap-2.5 border-b border-sidebar-fg/10 px-3",
          collapsed && "justify-center px-0",
        )}
      >
        <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-accent">
          <Cloud className="h-4 w-4 text-fg-on-accent" />
        </div>
        {!collapsed && (
          <span className="text-sm font-semibold tracking-tight text-sidebar-fg-strong">
            Overcast
          </span>
        )}
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto py-2">
        {/* Dashboard — pinned, not draggable */}
        <NavItemContent
          item={DASHBOARD_ITEM}
          collapsed={collapsed}
          pathname={pathname}
          effectiveExpanded={effectiveExpanded}
          toggleExpand={toggleExpand}
        />

        {/* Currently active service — shown contextually when not already in favourites */}
        {currentService && (
          <>
            <NavItemContent
              item={currentService}
              collapsed={collapsed}
              pathname={pathname}
              effectiveExpanded={effectiveExpanded}
              toggleExpand={toggleExpand}
            />
            {favourites.length > 0 && <div className="mx-3 my-1 border-t border-sidebar-fg/10" />}
          </>
        )}

        {/* Favourited services — sorted by the favourites array */}
        {favourites.length === 0 ? (
          !collapsed && (
            <p className="px-4 py-3 text-xs text-sidebar-fg/40">Star a service to pin it here</p>
          )
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCenter}
            onDragStart={({ active }) => setActiveId(active.id as string)}
            onDragEnd={handleDragEnd}
            onDragCancel={() => setActiveId(null)}
          >
            <SortableContext items={favourites} strategy={verticalListSortingStrategy}>
              {favourites.map((key) => {
                const item = SERVICE_BY_KEY[key]
                if (!item) return null
                return (
                  <SortableNavItem
                    key={key}
                    item={item}
                    collapsed={collapsed}
                    pathname={pathname}
                    effectiveExpanded={effectiveExpanded}
                    toggleExpand={toggleExpand}
                    isDragging={activeId === key}
                  />
                )
              })}
            </SortableContext>
          </DndContext>
        )}
      </nav>

      {/* Bottom nav (non-service pages) */}
      <nav className="shrink-0 border-t border-sidebar-fg/10 py-2">
        {bottomItems.map(({ to, label, icon: Icon, color }) => {
          const active = pathname.startsWith(to)
          const badgeCount = to === "/inbox" ? unreadCount : 0
          return (
            <Link
              key={to}
              to={to}
              className={cn(
                "relative mx-2 my-0.5 flex h-8 items-center gap-2.5 rounded-md px-2 text-sm font-medium transition-colors",
                active
                  ? "bg-sidebar-item-active text-sidebar-item-active-fg"
                  : "text-sidebar-fg hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
                collapsed && "mx-1 justify-center px-0",
              )}
              title={collapsed ? label : undefined}
            >
              <Icon className={cn("h-4 w-4 shrink-0", active ? "text-fg-on-accent" : color)} />
              {!collapsed && <span>{label}</span>}
              {badgeCount > 0 && (
                <span
                  className={cn(
                    "ml-auto min-w-5 rounded-full px-1.5 py-0.5 text-center text-[10px] leading-none font-semibold tabular-nums",
                    active
                      ? "bg-sidebar-item-active-fg/20 text-sidebar-item-active-fg"
                      : "bg-amber-400 text-black",
                    collapsed && "absolute -top-1 -right-1 ml-0",
                  )}
                  aria-label={`${badgeCount} unread inbox message${badgeCount === 1 ? "" : "s"}`}
                >
                  {badgeCount > 99 ? "99+" : badgeCount}
                </span>
              )}
            </Link>
          )
        })}
      </nav>

      {/* Collapse toggle */}
      <button
        onClick={() => setCollapsed((v) => !v)}
        className={cn(
          "flex h-9 items-center justify-center border-t border-sidebar-fg/10",
          "text-sidebar-fg transition-colors hover:bg-sidebar-item-hover hover:text-sidebar-fg-strong",
          "gap-1.5 text-sm",
        )}
        title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
      >
        {collapsed ? (
          <ChevronRight className="h-3.5 w-3.5" />
        ) : (
          <>
            <ChevronLeft className="h-3.5 w-3.5" />
            <span>Collapse</span>
          </>
        )}
      </button>
    </aside>
  )
}
