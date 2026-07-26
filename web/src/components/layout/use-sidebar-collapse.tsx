import { createContext, useCallback, useContext, useEffect, useState } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"

export const SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY = "overcast.sidebar.collapsed.narrow"
export const SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY = "overcast.sidebar.collapsed.wide"

export const NARROW_SIDEBAR_QUERY = "(max-width: 900px)"

interface SidebarCollapseContextValue {
  collapsed: boolean
  toggleCollapsed: () => void
}

const SidebarCollapseContext = createContext<SidebarCollapseContextValue | null>(null)

function getIsNarrowViewport() {
  if (typeof window === "undefined") return false
  try {
    return window.matchMedia(NARROW_SIDEBAR_QUERY).matches
  } catch {
    return false
  }
}

/**
 * Owns the sidebar collapse preference. Both the sidebar and the header read
 * it — the header shows the brand block only while the sidebar is collapsed —
 * so the state lives in context rather than in per-component storage state.
 */
export function SidebarCollapseProvider({ children }: { children: React.ReactNode }) {
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

  const setCollapsed = isNarrow ? setNarrowCollapsed : setWideCollapsed
  const toggleCollapsed = useCallback(() => {
    setCollapsed((value) => !value)
  }, [setCollapsed])

  return (
    <SidebarCollapseContext.Provider
      value={{ collapsed: isNarrow ? narrowCollapsed : wideCollapsed, toggleCollapsed }}
    >
      {children}
    </SidebarCollapseContext.Provider>
  )
}

/** Returns the persisted sidebar collapse state. */
// eslint-disable-next-line react-refresh/only-export-components
export function useSidebarCollapse(): SidebarCollapseContextValue {
  const value = useContext(SidebarCollapseContext)
  if (!value) throw new Error("useSidebarCollapse must be used within a SidebarCollapseProvider")
  return value
}
