import { Bug, Monitor, Moon, Settings, Sun } from "lucide-react"
import { cn } from "@/lib/utils"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useTheme } from "@/hooks/use-theme"
import { useDevTools } from "@/hooks/use-dev-tools"
import { endpointStore } from "@/services/endpoint-store"
import { RegionSelectCompact } from "@/components/ui/region-select"

const iconButtonCls = cn(
  "flex h-8 w-8 shrink-0 items-center justify-center rounded-control border border-border bg-bg",
  "text-fg-subtle transition-colors hover:border-accent hover:text-accent",
  "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
)

/**
 * Topbar action cluster.
 *
 * The tail — region select → theme toggle → settings — is identical on every
 * page so muscle memory holds. Settings is always present and always last.
 * Conditional or page-specific status controls (the dev-tools toggle here)
 * sit to the LEFT of the region select and never break into that tail.
 */
export function HeaderActions() {
  const endpoint = useEndpoint()
  const { theme, setTheme } = useTheme()
  const { open: devToolsActive, toggle: toggleDevTools } = useDevTools()

  const nextTheme = theme === "light" ? "dark" : theme === "dark" ? "system" : "light"
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor

  return (
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
        onClick={() => endpointStore.reset()}
        title="Change connection"
        aria-label="Change connection"
        className={iconButtonCls}
      >
        <Settings className="h-[15px] w-[15px]" />
      </button>
    </div>
  )
}
