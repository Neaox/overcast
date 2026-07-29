import { useState } from "react"
import {
  closestCenter,
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core"
import {
  arrayMove,
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { useDragClickGuard } from "@/hooks/use-drag-click-guard"
import { useFavourites } from "@/hooks/use-favourites"
import { restrictToVerticalAxis } from "@/lib/dnd"
import { ALL_SERVICES } from "@/lib/nav-services"
import { SidebarNavItem, type SidebarNavEntry } from "./sidebar-nav-item"

const SERVICE_BY_KEY = new Map(ALL_SERVICES.map((service) => [service.key, service]))

interface SidebarFavouritesProps {
  collapsed: boolean
  pathname: string
  isExpanded: (key: string) => boolean
  isEnabled: (route: string) => boolean
  onToggleExpand: (key: string) => void
}

/** Pinned services — reorderable by drag in the expanded sidebar. */
export function SidebarFavourites({
  collapsed,
  pathname,
  isExpanded,
  isEnabled,
  onToggleExpand,
}: SidebarFavouritesProps) {
  const { favourites, reorderFavourites } = useFavourites()
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))
  const markDragged = useDragClickGuard()

  function handleDragStart(id: string) {
    setDraggingId(id)
    // Keep the drop from activating the link under the pointer, wherever it lands.
    markDragged()
  }

  function handleDragEnd({ active, over }: DragEndEvent) {
    setDraggingId(null)
    if (over && active.id !== over.id) {
      const from = favourites.indexOf(active.id as string)
      const to = favourites.indexOf(over.id as string)
      reorderFavourites(arrayMove(favourites, from, to))
    }
  }

  if (favourites.length === 0) {
    return collapsed ? null : (
      <p className="px-2.5 py-2 font-mono text-[11px] text-fg-subtle">
        Star a service to pin it here
      </p>
    )
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      modifiers={[restrictToVerticalAxis]}
      onDragStart={({ active }) => handleDragStart(active.id as string)}
      onDragEnd={handleDragEnd}
      onDragCancel={() => setDraggingId(null)}
    >
      <SortableContext items={favourites} strategy={verticalListSortingStrategy}>
        {favourites.map((key) => {
          const item = SERVICE_BY_KEY.get(key)
          if (!item) return null
          return (
            <SortableFavourite
              key={key}
              item={item}
              collapsed={collapsed}
              pathname={pathname}
              disabled={!isEnabled(item.to)}
              expanded={isExpanded(item.to)}
              onToggleExpand={onToggleExpand}
              dragging={draggingId === key}
            />
          )
        })}
      </SortableContext>
    </DndContext>
  )
}

interface SortableFavouriteProps {
  item: SidebarNavEntry
  collapsed: boolean
  pathname: string
  disabled: boolean
  expanded: boolean
  onToggleExpand: (key: string) => void
  dragging: boolean
}

function SortableFavourite({ item, dragging, ...rest }: SortableFavouriteProps) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: item.to })

  return (
    <SidebarNavItem
      item={item}
      {...rest}
      sortable={{
        ref: setNodeRef,
        style: {
          transform: CSS.Transform.toString(transform),
          transition,
          opacity: dragging ? 0.4 : 1,
        },
        attributes,
        listeners,
      }}
    />
  )
}
