import { Link } from "@tanstack/react-router"
import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"
import type { SubNavChild, SubNavItem } from "@/lib/nav-services"
import { flatChildren, isGroup } from "./nav-children"

interface SidebarSubNavProps {
  items: SubNavChild[]
  pathname: string
  /** Target of the parent row's `aria-controls`. */
  id?: string
}

/** Nested routes of an expanded service item. */
export function SidebarSubNav({ items, pathname, id }: SidebarSubNavProps) {
  const activeTo = flatChildren(items)
    .filter((child) => pathname.startsWith(child.to))
    .sort((a, b) => b.to.length - a.to.length)[0]?.to

  function renderItem(child: SubNavItem) {
    return (
      <li key={child.to}>
      <Link
        to={child.to}
        className={cn(
          "flex items-center rounded-control px-2.5 py-1 font-mono text-xs transition-colors",
          child.to === activeTo
            ? "font-bold text-accent"
            : "text-fg-muted hover:bg-sidebar-item-hover hover:text-accent",
        )}
        aria-current={child.to === activeTo ? "page" : undefined}
      >
        {child.label}
      </Link>
      </li>
    )
  }

  return (
    <ul
      id={id}
      className="m-0 mt-px ml-4 flex list-none flex-col gap-px border-l border-border p-0 pl-2"
    >
      {items.map((child, index) =>
        isGroup(child) ? (
          <li key={child.group}>
            <p className={cn(sectionLabel, "px-2.5 pb-0.5 text-fg-subtle", index > 0 && "mt-2")}>
              {child.group}
            </p>
            <ul className="m-0 flex list-none flex-col gap-px p-0" aria-label={child.group}>
              {child.items.map(renderItem)}
            </ul>
          </li>
        ) : (
          renderItem(child)
        ),
      )}
    </ul>
  )
}
