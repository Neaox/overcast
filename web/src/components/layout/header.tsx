import { Settings, Sun, Moon, Monitor, Bug, Wifi } from "lucide-react"
import { useEndpoint } from "@/hooks/use-endpoint"
import { endpointStore } from "@/services/endpoint-store"
import { Button } from "@/components/ui/button"
import { RegionSelectCompact } from "@/components/ui/region-select"
import { useTheme } from "@/hooks/use-theme"
import { useDevTools } from "@/hooks/use-dev-tools"
import { useConnectionStatus } from "@/hooks/use-connection-status"
import { cn } from "@/lib/utils"
import { GlobalSearchTrigger } from "@/components/layout/global-search"

export function Header({ onSearchOpen }: { onSearchOpen: () => void }) {
  const endpoint = useEndpoint()
  const { theme, setTheme } = useTheme()

  const { open: devToolsActive, toggle: toggleDevTools } = useDevTools()
  const { isOnline } = useConnectionStatus()
  const nextTheme = theme === "light" ? "dark" : theme === "dark" ? "system" : "light"
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor

  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border bg-bg-elevated px-4">
      {/* Search trigger — takes available space on the left */}
      <GlobalSearchTrigger onClick={onSearchOpen} />

      {/* Endpoint label + connection status */}
      <div className="hidden items-center gap-1.5 sm:flex">
        <i
          title={
            isOnline === true ? "Connected" : isOnline === false ? "Disconnected" : "Connecting…"
          }
        >
          <Wifi
            className={cn(
              "h-3.5 w-3.5 shrink-0 transition-colors",
              isOnline === true ? "text-green-400" : "text-fg-subtle/50",
            )}
          />
        </i>
        <span className="max-w-xs truncate text-xs text-fg-subtle">
          {endpoint.label ?? endpoint.baseUrl}
        </span>
      </div>

      <div className="ml-auto flex items-center gap-2">
        <RegionSelectCompact
          value={endpoint.region}
          onChange={(region) => {
            endpointStore.set({ ...endpoint, region })
          }}
        />
        {import.meta.env.DEV && (
          <Button
            variant="ghost"
            size="icon"
            onClick={toggleDevTools}
            title="Toggle developer tools"
            className={cn(devToolsActive && "text-primary")}
          >
            <Bug className="h-4 w-4" />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setTheme(nextTheme)}
          title={`Switch to ${nextTheme} mode`}
        >
          <ThemeIcon className="h-4 w-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          onClick={() => endpointStore.reset()}
          title="Change connection"
        >
          <Settings className="h-4 w-4" />
        </Button>
      </div>
    </header>
  )
}
