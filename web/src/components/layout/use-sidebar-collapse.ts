import { useCallback, useEffect, useState } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"

export const SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY = "overcast.sidebar.collapsed.narrow"
export const SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY = "overcast.sidebar.collapsed.wide"

export const NARROW_SIDEBAR_QUERY = "(max-width: 900px)"

function getIsNarrowViewport() {
  if (typeof window === "undefined") return false
  try {
    return window.matchMedia(NARROW_SIDEBAR_QUERY).matches
  } catch {
    return false
  }
}

export function useSidebarCollapse() {
  const [isNarrow, setIsNarrow] = useState(getIsNarrowViewport)
  const [narrowCollapsed, setNarrowCollapsed] = useLocalStorage(
    SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY,
    true,
  )
  const [wideCollapsed, setWideCollapsed] = useLocalStorage(
    SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY,
    false,
  )

  useEffect(() => {
    const media = window.matchMedia(NARROW_SIDEBAR_QUERY)
    const update = () => setIsNarrow(media.matches)
    update()
    media.addEventListener("change", update)
    return () => media.removeEventListener("change", update)
  }, [])

  const collapsed = isNarrow ? narrowCollapsed : wideCollapsed
  const setCollapsed = isNarrow ? setNarrowCollapsed : setWideCollapsed
  const toggleCollapsed = useCallback(() => {
    setCollapsed((value) => !value)
  }, [setCollapsed])

  return { collapsed, toggleCollapsed }
}
