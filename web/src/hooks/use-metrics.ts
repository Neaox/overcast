/**
 * useMetrics — polls /_metrics every POLL_INTERVAL_MS and accumulates a
 * rolling history of snapshots for rendering sparklines.
 */
import { useState, useEffect, useCallback, useRef } from "react"
import { metrics } from "@/services/api"
import type { MetricsSnapshot } from "@/types"

const POLL_INTERVAL_MS = 3_000
const MAX_HISTORY = 60 // 3 minutes at 3s intervals

export interface MetricsHistory {
  snapshots: MetricsSnapshot[]
  latest: MetricsSnapshot | null
  error: string | null
}

export function useMetrics(): MetricsHistory {
  const [snapshots, setSnapshots] = useState<MetricsSnapshot[]>([])
  const [error, setError] = useState<string | null>(null)
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const poll = useCallback(async () => {
    try {
      const snap = await metrics.get()
      setError(null)
      setSnapshots((prev) => {
        const next = [...prev, snap]
        return next.length > MAX_HISTORY ? next.slice(next.length - MAX_HISTORY) : next
      })
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to fetch metrics")
    }
  }, [])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void poll()
    intervalRef.current = setInterval(poll, POLL_INTERVAL_MS)
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current)
    }
  }, [poll])

  return {
    snapshots,
    latest: snapshots.length > 0 ? snapshots[snapshots.length - 1] : null,
    error,
  }
}
