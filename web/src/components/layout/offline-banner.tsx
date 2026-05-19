import { WifiOff } from "lucide-react"
import { useConnectionStatus } from "@/hooks/use-connection-status"

/**
 * A sticky banner rendered at the top of the app when the backend
 * emulator is unreachable. Informational only — the UI stays visible
 * in readonly mode with its cached data.
 */
export function OfflineBanner() {
  const { isOnline } = useConnectionStatus()

  // null = still connecting; don't flash the banner during initial load.
  if (isOnline !== false) return null

  return (
    <div
      role="status"
      className="flex items-center justify-center gap-2 bg-warning px-3 py-1.5 text-sm font-medium text-black"
    >
      <WifiOff className="h-4 w-4 shrink-0" />
      <span>
        Connection to the emulator lost — showing cached data. The UI will reconnect automatically.
      </span>
    </div>
  )
}
