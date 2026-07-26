import { useEffect, useState } from "react"
import { Clock } from "lucide-react"
import { cn } from "@/lib/utils"
import { OvercastMark } from "@/components/brand/overcast-mark"
import { OvercastWordmark } from "@/components/brand/overcast-wordmark"
import { OvercastLoader } from "@/components/brand/overcast-loader"

/** How long the attempt runs before the retry affordance offers a way out. */
export const RETRY_AFTER_MS = 5000

/** Reserved up-front so the chip's arrival never nudges the loader. */
const CHIP_HEIGHT = "h-[29px]"

interface ConnectingScreenProps {
  /** Host of the configured endpoint, e.g. `localhost:4566`. */
  host: string
  /** Re-issues the liveness probe. */
  onRetry: () => void
  /** Delay before the retry affordance appears. */
  retryAfterMs?: number
}

/**
 * Cold boot — configured, but the emulator has not answered yet.
 *
 * Deliberately bare: a centred column straight on `--bg` with no card and no
 * panel, and a topbar reduced to brand + amber dot + host. Search, breadcrumbs,
 * region and theme are *removed* rather than disabled, so arriving at the shell
 * is a fill-in rather than a re-layout.
 */
export function ConnectingScreen({
  host,
  onRetry,
  retryAfterMs = RETRY_AFTER_MS,
}: ConnectingScreenProps) {
  // `attempt` re-arms the timer when the user retries; the effect owns a real
  // external system (a timer), which is what effects are for.
  const [attempt, setAttempt] = useState(0)
  const [showRetry, setShowRetry] = useState(false)

  useEffect(() => {
    const timer = setTimeout(() => setShowRetry(true), retryAfterMs)
    return () => clearTimeout(timer)
  }, [attempt, retryAfterMs])

  function handleRetry() {
    setShowRetry(false)
    setAttempt((n) => n + 1)
    onRetry()
  }

  return (
    <div className="fixed inset-0 flex flex-col bg-bg">
      <header className="flex h-13 shrink-0 items-center gap-4 border-b border-border bg-bg-elevated px-4">
        {/* No divider and no right padding — the connected topbar's rule only
            exists to separate the brand from the breadcrumbs, and there are none. */}
        <div className="flex h-6 shrink-0 items-center gap-2">
          <OvercastMark size={22} />
          <OvercastWordmark size="sm" />
        </div>
        <p className="flex min-w-0 items-center gap-2 font-mono text-xs text-fg-subtle">
          <span aria-hidden="true" className="h-[7px] w-[7px] shrink-0 rounded-full bg-warning" />
          <span className="truncate">{host}</span>
        </p>
      </header>

      <div
        role="status"
        aria-live="polite"
        className="flex flex-1 flex-col items-center justify-center gap-4"
      >
        <OvercastLoader size={72} />

        <div className="flex flex-col items-center gap-1.5 px-4 text-center">
          <span className="font-mono text-[13px] text-fg">connecting to {host}</span>
          <span className="text-xs text-fg-subtle">
            Reading emulator state — first boot can take a few seconds.
          </span>
        </div>

        <div className={cn("flex items-center", CHIP_HEIGHT)}>
          {showRetry && (
            <button
              type="button"
              onClick={handleRetry}
              aria-label="Retry connecting"
              className={cn(
                "flex items-center gap-2 rounded-control border border-border bg-bg-elevated px-3 py-[7px]",
                "font-mono text-[11px] text-fg-subtle transition-colors",
                "hover:border-accent hover:text-accent",
                "focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none",
              )}
            >
              <Clock className="h-[13px] w-[13px] shrink-0" strokeWidth={1.75} />
              still working after {Math.round(retryAfterMs / 1000)}s · retry
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
