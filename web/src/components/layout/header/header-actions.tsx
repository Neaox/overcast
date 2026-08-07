import { useState } from "react"
import { Bug, Monitor, Moon, Plug, Sun } from "lucide-react"
import { cn } from "@/lib/utils"
import { useEndpoint, useIsConfigured } from "@/hooks/use-endpoint"
import { useTheme } from "@/hooks/use-theme"
import { useDevTools } from "@/hooks/use-dev-tools"
import { endpointStore } from "@/services/endpoint-store"
import { RegionSelectCompact } from "@/components/ui/region-select"
import { ConnectionDialogModal } from "@/components/layout/connection-dialog"

const iconButtonCls = cn(
  "flex h-8 w-8 shrink-0 items-center justify-center rounded-control border border-border bg-bg",
  "text-fg-subtle transition-colors hover:border-accent hover:text-accent",
  "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
)

export function HeaderActions() {
  const endpoint = useEndpoint()
  const { theme, setTheme } = useTheme()
  const { open: devToolsActive, toggle: toggleDevTools } = useDevTools()
  const configured = useIsConfigured()
  const [showConnectionModal, setShowConnectionModal] = useState(false)

  const nextTheme = theme === "light" ? "dark" : theme === "dark" ? "system" : "light"
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor

  return (
    <>
      <div className="flex shrink-0 items-center gap-2">
        {import.meta.env.DEV && (
          <button
            onClick={toggleDevTools}
            title="Toggle developer tools"
            aria-label="Toggle developer tools"
            className={cn(iconButtonCls, devToolsActive && "border-accent text-accent")}
          >
            <Bug className="h-[15px] w-[15px]" />
          </button>
        )}
        <RegionSelectCompact
          value={endpoint.region}
          onChange={(region) => endpointStore.set({ ...endpoint, region })}
        />
        <button
          onClick={() => setTheme(nextTheme)}
          title={`Switch to ${nextTheme} mode`}
          aria-label={`Switch to ${nextTheme} mode`}
          className={iconButtonCls}
        >
          <ThemeIcon className="h-[15px] w-[15px]" />
        </button>
        <button
          onClick={() => {
            if (configured) {
              setShowConnectionModal(true)
            } else {
              endpointStore.reset()
            }
          }}
          title="Connection settings"
          aria-label="Connection settings"
          className={iconButtonCls}
        >
          <Plug className="h-[15px] w-[15px]" />
        </button>
      </div>
      {showConnectionModal && (
        <ConnectionDialogModal onClose={() => setShowConnectionModal(false)} />
      )}
    </>
  )
}
