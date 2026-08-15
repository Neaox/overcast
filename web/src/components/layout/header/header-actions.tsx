import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { Bug, Monitor, Moon, Plug, Settings, Sun } from "lucide-react"
import { cn } from "@/lib/utils"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useTheme } from "@/hooks/use-theme"
import { useDevTools } from "@/hooks/use-dev-tools"
import { endpointStore } from "@/services/endpoint-store"
import { isEndpointConfigurable } from "@/services/discovery"
import { RegionSelectCompact } from "@/components/ui/region-select"
import { ConnectionDialogModal } from "@/components/layout/connection-dialog"

const iconButtonCls = cn(
  "flex h-8 w-8 shrink-0 items-center justify-center rounded-control border border-border bg-bg",
  "text-fg-subtle transition-colors hover:border-accent hover:text-accent",
  "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
)

/**
 * Topbar action cluster.
 *
 * The tail — region select → theme toggle → settings — is identical on every
 * page so muscle memory holds, and settings is always last within it.
 * Conditional or page-specific controls (the dev-tools toggle and the
 * connection quick-edit here) sit to the LEFT of the region select and never
 * break into that tail.
 *
 * Two connection affordances, deliberately: the plug edits the endpoint in a
 * modal from wherever you are, which is worth a click of its own when the
 * console is pointed at the wrong daemon; the gear opens /settings, which owns
 * the same form plus HTTPS and whatever comes next. Both render the identical
 * ConnectionForm, so neither can drift from the other. The plug is hidden in
 * bundled builds, where the endpoint is derived from window.location and there
 * is nothing to edit — the gear still exists there, because HTTPS does.
 */
export function HeaderActions() {
  const endpoint = useEndpoint()
  const { theme, setTheme } = useTheme()
  const { open: devToolsActive, toggle: toggleDevTools } = useDevTools()
  const [showConnectionModal, setShowConnectionModal] = useState(false)
  const connectionEditable = isEndpointConfigurable()

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
        {connectionEditable && (
          <button
            onClick={() => setShowConnectionModal(true)}
            title="Connection settings"
            aria-label="Connection settings"
            className={iconButtonCls}
          >
            <Plug className="h-[15px] w-[15px]" />
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
        <Link to="/settings" title="Settings" aria-label="Settings" className={iconButtonCls}>
          <Settings className="h-[15px] w-[15px]" />
        </Link>
      </div>
      {showConnectionModal && (
        <ConnectionDialogModal onClose={() => setShowConnectionModal(false)} />
      )}
    </>
  )
}
