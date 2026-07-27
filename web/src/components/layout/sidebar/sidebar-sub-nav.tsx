import { Link } from "@tanstack/react-router"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { SubNavChild, SubNavItem } from "@/lib/nav-services"
import { flatChildren, isGroup } from "./nav-children"

interface SidebarSubNavProps {
  items: SubNavChild[]
  pathname: string
}

/** Nested routes of an expanded service item. */
export function SidebarSubNav({ items, pathname }: SidebarSubNavProps) {
  const activeTo = flatChildren(items)
    .filter((child) => pathname.startsWith(child.to))
    .sort((a, b) => b.to.length - a.to.length)[0]?.to

  function renderItem(child: SubNavItem) {
    return (
      <Link
        key={child.to}
        to={child.to}
        className={cn(
          "flex items-center rounded-control px-2.5 py-1 font-mono text-xs transition-colors",
          child.to === activeTo
            ? "font-bold text-accent"
            : "text-fg-muted hover:bg-sidebar-item-hover hover:text-accent",
        )}
      >
        {child.label}
      </Link>
    )
  }

  return (
    <div className="mt-px ml-4 flex flex-col gap-px border-l border-border pl-2">
      {items.map((child, index) =>
        isGroup(child) ? (
          <div key={child.group} className="flex flex-col gap-px">
            <p className={cn(sectionLabel, "px-2.5 pb-0.5 text-fg-subtle", index > 0 && "mt-2")}>
              {child.group}
            </p>
            {child.items.map(renderItem)}
          </div>
        ) : (
          renderItem(child)
        ),
      )}
    </div>
  )
}
