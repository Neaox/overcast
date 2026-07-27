import { OvercastMark } from "@/components/brand/overcast-mark"
import { OvercastWordmark } from "@/components/brand/overcast-wordmark"

/** Brand row at the top of the expanded sidebar. */
export function SidebarBrand() {
  return (
    <div className="flex shrink-0 items-center gap-2 px-4 pt-2 pb-4">
      <OvercastMark size={24} />
      <OvercastWordmark />
    </div>
  )
}
