import { createContext, useCallback, useContext, useMemo } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"

export const SERVICE_ICON_COLOR_STORAGE_KEY = "overcast.serviceIconColor"

/**
 * Default ON for the service-icon-colours preview: the categorical ramp
 * (`--cat-1…10`, `web/src/styles/global.css`) already carries a distinct hue
 * per service in both themes, and `service-registry.ts` already assigns every
 * service a slot — this setting only decides whether consuming components
 * apply it to the icon or fall back to the neutral accent treatment the
 * design-system rollout (#314) shipped with.
 */
const DEFAULT_ENABLED = true

interface ServiceIconColorContextValue {
  enabled: boolean
  setEnabled: (enabled: boolean) => void
  toggle: () => void
}

const ServiceIconColorContext = createContext<ServiceIconColorContextValue | null>(null)

/**
 * Owns the "colour service icons" preference. Every surface that renders a
 * service icon (sidebar, dashboard tiles, global search, mega menu) reads it
 * from here rather than from its own `useLocalStorage` instance, so flipping
 * the switch in Settings repaints the sidebar immediately instead of waiting
 * for it to remount — the same reason `use-sidebar-collapse.tsx` is a
 * provider rather than a bare hook.
 */
export function ServiceIconColorProvider({ children }: { children: React.ReactNode }) {
  const [enabled, setEnabled] = useLocalStorage(SERVICE_ICON_COLOR_STORAGE_KEY, DEFAULT_ENABLED)

  const toggle = useCallback(() => {
    setEnabled((value) => !value)
  }, [setEnabled])

  const value = useMemo(() => ({ enabled, setEnabled, toggle }), [enabled, setEnabled, toggle])

  return (
    <ServiceIconColorContext.Provider value={value}>{children}</ServiceIconColorContext.Provider>
  )
}

/** Returns the persisted "colour service icons" preference. */
// eslint-disable-next-line react-refresh/only-export-components
export function useServiceIconColor(): ServiceIconColorContextValue {
  const value = useContext(ServiceIconColorContext)
  if (!value) {
    throw new Error("useServiceIconColor must be used within a ServiceIconColorProvider")
  }
  return value
}
