import { cn } from "@/lib/utils"
import { useEndpoint } from "@/hooks/use-endpoint"
import { useConnectionStatus } from "@/hooks/use-connection-status"
import { CopyButton } from "@/components/ui/copy-button"

export function HeaderEndpoint() {
  const endpoint = useEndpoint()
  const { isOnline } = useConnectionStatus()

  return (
    <span className="hidden shrink-0 items-center gap-1.5 font-mono text-xs sm:flex">
      <span
        className={cn(
          "h-[7px] w-[7px] shrink-0 rounded-full",
          isOnline === true ? "bg-success" : isOnline === false ? "bg-warning" : "bg-fg-subtle",
        )}
        title={
          isOnline === true ? "Connected" : isOnline === false ? "Disconnected" : "Connecting…"
        }
      />
      <span className="shrink-0 text-fg-subtle">{endpoint.label ?? endpoint.baseUrl}</span>
      <CopyButton
        value={endpoint.baseUrl}
        noun="endpoint"
        tone="inline"
      />
    </span>
  )
}
