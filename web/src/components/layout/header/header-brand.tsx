import { OvercastMark } from "@/components/brand/overcast-mark"
import { OvercastWordmark } from "@/components/brand/overcast-wordmark"

/** Brand block — shown in the topbar only while the sidebar is a collapsed rail. */
export function HeaderBrand() {
  return (
    <div className="flex h-6 shrink-0 items-center gap-2 border-r border-border pr-3">
      <OvercastMark size={22} />
      <OvercastWordmark size="sm" />
    </div>
  )
}
