import { cn } from "@/lib/utils"

/**
 * The wordless answer to "is the tail actually connected?".
 *
 * Tailing is a push stream — one long-lived StartLiveTail session, no polling
 * — so what the user needs to see is not activity but *health*: a session that
 * died (emulator restart, dropped network) otherwise looks exactly like a
 * healthy session on a quiet stream. Green pulse: session open, events arrive
 * the moment the emulator writes them. Red, still: the session is gone and
 * what is on screen has stopped moving.
 */
export function LiveTailIndicator({
  status,
  className,
}: {
  status: "live" | "error"
  className?: string
}) {
  return (
    <span
      className={cn("relative flex h-2 w-2 shrink-0", className)}
      data-testid="live-tail-indicator"
      data-status={status}
      aria-hidden="true"
    >
      {status === "live" && (
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-60" />
      )}
      <span
        className={cn(
          "relative inline-flex h-2 w-2 rounded-full",
          status === "live" ? "bg-success" : "bg-danger",
        )}
      />
    </span>
  )
}
