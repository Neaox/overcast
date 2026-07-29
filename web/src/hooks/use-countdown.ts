import { useEffect, useRef, useState } from "react"

/**
 * useCountdown — counts down seconds until a future timestamp, calling
 * `onExpired` exactly once when the countdown reaches zero.
 *
 * The deadline is read on mount and then once a second. A caller that swaps in
 * a *new* deadline mid-countdown should key the component on it, so the hook
 * remounts and reads it straight away rather than showing the outgoing
 * countdown's last value until the next tick.
 *
 * @param visibleAfter — Unix-millis timestamp to count down to.
 * @param onExpired — optional callback when the countdown hits zero.
 * @returns secondsLeft — whole seconds remaining (≥ 0).
 */
export function useCountdown(visibleAfter: number, onExpired?: () => void): number {
  const [secondsLeft, setSecondsLeft] = useState(() => remainingSeconds(visibleAfter))
  const firedRef = useRef(false)
  const onExpiredRef = useRef(onExpired)
  useEffect(() => {
    onExpiredRef.current = onExpired
  })

  useEffect(() => {
    firedRef.current = false
    const id = setInterval(() => {
      const remaining = remainingSeconds(visibleAfter)
      setSecondsLeft(remaining)
      if (remaining <= 0) {
        clearInterval(id)
        if (!firedRef.current) {
          firedRef.current = true
          onExpiredRef.current?.()
        }
      }
    }, 1000)
    return () => clearInterval(id)
  }, [visibleAfter])

  return secondsLeft
}

/** Whole seconds until `deadline` (epoch ms), never negative. */
function remainingSeconds(deadline: number): number {
  return Math.max(0, Math.ceil((deadline - Date.now()) / 1000))
}
