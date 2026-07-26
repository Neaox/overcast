import { sectionLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

interface SidebarSectionProps {
  label: string
  collapsed: boolean
  /** The first section renders without the leading rail divider / extra gap. */
  first?: boolean
  children: React.ReactNode
}

/**
 * A titled group of nav items. The collapsed rail has no room for headings,
 * so sections are separated by a hairline divider instead.
 */
export function SidebarSection({ label, collapsed, first, children }: SidebarSectionProps) {
  return (
    <div className={cn("flex flex-col", collapsed ? "w-full items-center gap-0.5" : "gap-px")}>
      {collapsed ? (
        !first && <div className="my-2 h-px w-6 bg-border" />
      ) : (
        <div className={cn(sectionLabel, "px-2 pb-1 text-fg-subtle", first ? "pt-2" : "pt-4")}>
          {label}
        </div>
      )}
      {children}
    </div>
  )
}
