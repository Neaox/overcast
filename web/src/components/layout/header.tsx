import { Settings, Sun, Moon, Monitor } from "lucide-react"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import { RegionSelectCompact } from "@/components/ui/region-select"
import { useTheme } from "@/hooks/use-theme"
import { cn } from "@/lib/utils"

export function Header() {
  const { endpoint, setEndpoint, reset } = useEndpoint()
  const { theme, setTheme } = useTheme()

  const nextTheme = theme === "light" ? "dark" : theme === "dark" ? "system" : "light"
  const ThemeIcon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor

  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border bg-bg-elevated px-4">
      <RegionSelectCompact
        value={endpoint.region}
        onChange={(region) => setEndpoint({ ...endpoint, region })}
      />

      {/* Endpoint label */}
      <span className={cn("hidden max-w-xs truncate text-xs text-fg-subtle sm:block")}>
        {endpoint.label ?? endpoint.baseUrl}
      </span>

      <div className="ml-auto flex items-center gap-1">
        <Button
          variant="ghost"
          size="icon"
          onClick={() => setTheme(nextTheme)}
          title={`Switch to ${nextTheme} mode`}
        >
          <ThemeIcon className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" onClick={reset} title="Change connection">
          <Settings className="h-4 w-4" />
        </Button>
      </div>
    </header>
  )
}
