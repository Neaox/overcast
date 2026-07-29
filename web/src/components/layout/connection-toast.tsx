import { useCallback, useEffect, useRef } from "react"
import { createPortal } from "react-dom"
import { Link } from "@tanstack/react-router"
import { WifiOff } from "lucide-react"
import { TOAST_BAR, TOAST_CARD, useToast, useToastDockFooter } from "@/components/ui/toast"
import { BlinkingCursor } from "@/components/ui/skeleton"
import { useConnectionStatus } from "@/hooks/use-connection-status"
import { useCountdown } from "@/hooks/use-countdown"
import { useEndpointHost } from "@/hooks/use-endpoint"
import { formatTimeOfDay } from "@/lib/format"
import { cn } from "@/lib/utils"

/**
 * Persistent toast shown while the emulator is unreachable — the corner-most
 * item in the toast dock, so transient toasts stack above it.
 *
 * It replaced a full-width bar across the top of the shell, which cost every
 * page a row of layout at the moment the app had least to say, and said only
 * that something was wrong. A toast keeps the chrome intact and has room for
 * what a developer actually wants: *when the next attempt is*, how stale the
 * view has gone, and a way to skip the wait.
 *
 * ## Nothing here is allowed to jump
 *
 * A box that resizes every second in the corner of the eye is worse than no
 * box. So: a fixed dock width; single-line rows that `truncate` rather than
 * wrap, whatever the host is called or the locale does to a timestamp; digits
 * set `tabular-nums`; and the two things that move — the caret and the
 * draining hairline — are a fixed-size element and an absolutely positioned
 * one, neither on the layout path. The card is the same size on every frame
 * from the drop to the reconnect.
 */
export function ConnectionToast() {
  const { isOnline } = useConnectionStatus()
  const host = useEndpointHost()
  const dockFooter = useToastDockFooter()

  useReconnectedToast(isOnline, host)

  // `null` is a stream that is still opening — the pre-answer render and the
  // SSE handshake behind the connection gate's cold-boot screen. This toast is
  // only ever about a connection that stopped delivering.
  if (isOnline !== false || !dockFooter) return null

  return createPortal(<ReconnectingToast host={host} />, dockFooter)
}

/**
 * Closes the loop with an ordinary transient toast when the stream comes back.
 *
 * Without one, a reconnect is only ever a *disappearance* — and the moment it
 * matters most is when the user was not watching, came back to a UI with no
 * warning on it, and has no way to tell whether what they are reading is live
 * or the cache it was showing five minutes ago.
 *
 * Only a return is announced. The first connection of a session is the
 * connecting screen's business, so `null → true` stays quiet — which is why
 * "still opening" has to reach here as `null` and not as `false`; see
 * `connectionOf` in use-connection-status.
 */
function useReconnectedToast(isOnline: boolean | null, host: string): void {
  const { toast } = useToast()
  const droppedRef = useRef(false)

  useEffect(() => {
    if (isOnline === false) {
      droppedRef.current = true
      return
    }
    if (isOnline !== true || !droppedRef.current) return
    droppedRef.current = false
    toast({ title: "Reconnected", description: host, variant: "success" })
  }, [isOnline, host, toast])
}

function ReconnectingToast({ host }: { host: string }) {
  const { attempt, nextAttemptAt, lastConnectedAt, retryNow } = useConnectionStatus()

  return (
    <section aria-label="Connection status" className={cn(TOAST_CARD, "text-fg")}>
      <span aria-hidden className={cn(TOAST_BAR, "bg-warning")} />
      <WifiOff
        aria-hidden
        strokeWidth={2}
        className="mt-px h-[15px] w-[15px] shrink-0 text-warning"
      />

      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        {/* Announced once, on the drop. The visible copy below re-states it and
            is hidden from assistive tech, so a per-second countdown is never
            read out. */}
        <p role="status" className="sr-only">
          Lost connection to {host}. Reconnecting automatically — cached data is shown and writes
          are paused.
        </p>

        <p aria-hidden className="flex items-baseline font-mono text-xs font-bold">
          <span className="truncate">Reconnecting to {host}</span>
          <BlinkingCursor className="ml-1 bg-warning" />
        </p>

        {/* Keyed on the schedule so a fresh one restarts the countdown rather
            than showing the outgoing one's last value for up to a second. */}
        <RetrySchedule key={String(nextAttemptAt)} attempt={attempt} dueAt={nextAttemptAt} />

        {/* There is always a stamp to show — the card is only ever up because
            a live connection dropped, and the open before it recorded one. The
            other arm is for a state the worker should not be able to report. */}
        <p aria-hidden className="truncate text-[11px] text-fg-subtle">
          {lastConnectedAt
            ? `Cached at ${formatTimeOfDay(lastConnectedAt)} · writes paused`
            : "Writes paused until reconnected"}
        </p>

        <div className="mt-1.5 flex items-center gap-3 font-mono text-[11px]">
          <button type="button" onClick={retryNow} className={LINK}>
            retry now
          </button>
          {/* The event stream is buffered in the tab, so it still reads while
              the emulator is away — unlike anything that would have to fetch. */}
          <Link to="/events" className={LINK}>
            view events
          </Link>
        </div>
      </div>

      {nextAttemptAt !== null && <DrainBar key={nextAttemptAt} dueAt={nextAttemptAt} />}
    </section>
  )
}

const LINK =
  "text-accent transition-colors hover:text-accent-hover focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"

/**
 * `Attempt 3 · next in 4s` — the only part of the toast that re-renders per
 * second, which is why it is a component of its own.
 */
function RetrySchedule({ attempt, dueAt }: { attempt: number; dueAt: number | null }) {
  const secondsLeft = useCountdown(dueAt ?? 0)

  // Once the countdown is spent the attempt is due, so it reads as in flight
  // until the worker publishes the next schedule a moment later — rather than
  // sitting on "next in 0s" or bouncing back up to 1.
  const schedule = dueAt !== null && secondsLeft > 0 ? `next in ${secondsLeft}s` : "connecting…"

  return (
    <p aria-hidden className="truncate font-mono text-[11px] text-fg-muted tabular-nums">
      {attempt > 0 ? `Attempt ${attempt} · ${schedule}` : schedule}
    </p>
  )
}

/**
 * The countdown, drawn: a hairline along the bottom edge that empties as the
 * wait does.
 *
 * Timed as it attaches rather than on every render — the caller keys it on
 * `dueAt`, so that happens exactly once per scheduled attempt, and ticking the
 * countdown beside it cannot re-time (and so jog) a running animation.
 */
function DrainBar({ dueAt }: { dueAt: number }) {
  const time = useCallback(
    (node: HTMLElement | null) => {
      if (node) node.style.animationDuration = `${Math.max(0, dueAt - Date.now())}ms`
    },
    [dueAt],
  )

  return (
    <span aria-hidden className="pointer-events-none absolute inset-x-0 bottom-0 h-px bg-border">
      <span ref={time} className="oc-retry-drain block h-full bg-warning" />
    </span>
  )
}
