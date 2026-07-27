import type { SubNavChild, SubNavGroup, SubNavItem } from "@/lib/nav-services"

export function isGroup(child: SubNavChild): child is SubNavGroup {
  return "group" in child
}

/** Flattens grouped sub-navigation into a single list of leaf items. */
export function flatChildren(children: SubNavChild[]): SubNavItem[] {
  return children.flatMap((child) => (isGroup(child) ? child.items : [child]))
}
