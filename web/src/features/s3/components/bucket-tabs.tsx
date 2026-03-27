/**
 * BucketTabs — shared tab bar for bucket detail pages.
 *
 * Renders a two-tab strip (Objects | Configuration) and navigates between
 * sibling routes. Import in both bucket-detail.tsx and bucket-config.tsx.
 */
import { Link } from "@tanstack/react-router"
import { cn } from "@/lib/utils"

interface BucketTabsProps {
  bucket: string
  active: "objects" | "config"
}

export function BucketTabs({ bucket, active }: BucketTabsProps) {
  const tab =
    "inline-flex items-center gap-1.5 border-b-2 px-1 pb-2 text-sm font-medium transition-colors"
  const activeTab = "border-accent text-accent"
  const inactiveTab = "border-transparent text-fg-muted hover:text-fg"

  return (
    <div className="flex gap-6 border-b border-border">
      <Link
        to="/s3/$bucket"
        params={{ bucket }}
        className={cn(tab, active === "objects" ? activeTab : inactiveTab)}
      >
        Objects
      </Link>
      <Link
        to="/s3/$bucket/config"
        params={{ bucket }}
        className={cn(tab, active === "config" ? activeTab : inactiveTab)}
      >
        Configuration
      </Link>
    </div>
  )
}
