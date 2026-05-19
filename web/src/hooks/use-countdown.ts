import { useEffect, useRef, useState } from "react"

/**
 * useCountdown — counts down seconds until a future timestamp, calling
 * `onExpired` exactly once when the countdown reaches zero.
 *
 * @param visibleAfter — Unix-millis timestamp to count down to.
 * @param onExpired — optional callback when the countdown hits zero.
 * @returns secondsLeft — whole seconds remaining (≥ 0).
 */
export function useCountdown(visibleAfter: number, onExpired?: () => void): number {
  const [secondsLeft, setSecondsLeft] = useState(() =>
    Math.max(0, Math.ceil((visibleAfter - Date.now()) / 1000)),
  )
  const firedRef = useRef(false)
  const onExpiredRef = useRef(onExpired)
  useEffect(() => {
    onExpiredRef.current = onExpired
  })

  useEffect(() => {
    firedRef.current = false
    const id = setInterval(() => {
      const remaining = Math.max(0, Math.ceil((visibleAfter - Date.now()) / 1000))
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
