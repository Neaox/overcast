/**
 * The state half of a live tail: a bounded, ordered buffer of what the session
 * has pushed, fed once per animation frame.
 *
 * The viewers used to append one state update per log event, so every arriving
 * line re-ran the whole merge/sort/derive pipeline over the entire history —
 * the per-event cost grew with the buffer, and the UI ground to a halt exactly
 * when the stream got busy. Here arrivals collect in a plain array and fold
 * into React state at most once per frame; between frames they cost a push.
 *
 * The buffer is also bounded (`cap`, default the emulator's own 10,000-event
 * page limit) because a tail left open overnight must not grow without limit.
 * What the cap drops is counted, not silently swallowed: the AWS console shows
 * a "% displayed" figure when Live Tail samples, and an emulator's viewer owes
 * its user the same honesty.
 */

import { useCallback, useEffect, useState } from "react"
import {
  compareLogEvents,
  mergeSortedEvents,
  tailLogEvents,
  type TailedLogEvent,
} from "@/features/cloudwatch/logs/tail"

export interface LogTailBufferOptions {
  /** Whether a session should be open at all — the viewer's Tail toggle. */
  enabled: boolean
  groupIdentifier: string | undefined
  streamName?: string
  filterPattern?: string
  /** Most events held; beyond this the oldest are dropped and counted. */
  cap?: number
}

export interface LogTailBuffer {
  /** What the session has pushed, oldest first, at most `cap` events. */
  events: TailedLogEvent[]
  /** How many events the cap has pushed out since the buffer last reset. */
  overflowed: number
  /**
   * Whether a live session is open. "error" means the session died without
   * being asked to — an emulator restart, a dropped network — which otherwise
   * looks exactly like a healthy session on a quiet stream. Toggling the tail
   * off and on opens a fresh session and clears the error.
   */
  status: "idle" | "live" | "error"
  /** Empty the buffer without hanging up the session. */
  clear: () => void
}

interface BufferState {
  events: TailedLogEvent[]
  overflowed: number
}

const EMPTY: BufferState = { events: [], overflowed: 0 }

/** Fold one frame's arrivals into the buffer, keeping order and the cap. */
function appendBatch(prev: BufferState, batch: TailedLogEvent[], cap: number): BufferState {
  // A session pushes in near-chronological order, so the batch usually sorts
  // as a no-op and usually belongs entirely after what is already held —
  // making the common case two cheap checks and a concat.
  batch.sort(compareLogEvents)
  const last = prev.events.at(-1)
  const events =
    last === undefined || compareLogEvents(last, batch[0]) <= 0
      ? [...prev.events, ...batch]
      : mergeSortedEvents(prev.events, batch)
  if (events.length <= cap) return { events, overflowed: prev.overflowed }
  const dropped = events.length - cap
  return { events: events.slice(dropped), overflowed: prev.overflowed + dropped }
}

export function useLogTailBuffer({
  enabled,
  groupIdentifier,
  streamName,
  filterPattern,
  cap = 10_000,
}: LogTailBufferOptions): LogTailBuffer {
  const [buffer, setBuffer] = useState<BufferState>(EMPTY)
  const [died, setDied] = useState(false)

  // A buffer belongs to one stream identity. When the identity changes the
  // old events are another stream's, so they go — but `enabled` is not part
  // of identity: toggling the tail off and on keeps what it already caught.
  useEffect(() => {
    return () => setBuffer(EMPTY)
  }, [groupIdentifier, streamName, filterPattern])

  useEffect(() => {
    if (!enabled || !groupIdentifier) return

    // Each session opening starts healthy — a fresh session is how the user
    // recovers from a dead one (toggle the tail off and on).
    setDied(false)

    const controller = new AbortController()
    // Arrivals stage here between frames; only `flush` touches React state.
    let pending: TailedLogEvent[] = []
    let frame: number | null = null

    const flush = () => {
      frame = null
      if (pending.length === 0) return
      const batch = pending
      pending = []
      setBuffer((prev) => appendBatch(prev, batch, cap))
    }

    void (async () => {
      try {
        for await (const event of tailLogEvents({
          groupIdentifier,
          streamName,
          filterPattern,
          signal: controller.signal,
        })) {
          pending.push(event)
          frame ??= requestAnimationFrame(flush)
        }
      } catch (err) {
        // A session dies mid-stream when the emulator restarts or the network
        // drops — ordinary events in a dev loop, and nothing an effect body
        // can catch once it has escaped here as an unhandled rejection. Left
        // only in the console, a dead session looks exactly like a healthy one
        // on a quiet stream, so the death is state the viewers can show.
        if (!controller.signal.aborted) {
          console.warn("live tail session ended unexpectedly", err)
          setDied(true)
        }
      }
    })()

    return () => {
      controller.abort()
      if (frame != null) cancelAnimationFrame(frame)
    }
  }, [enabled, groupIdentifier, streamName, filterPattern, cap])

  // An event staged but not yet flushed when Clear fires will still appear on
  // the next frame. That is at most one frame's worth of events, all within
  // ~16 ms of the click — indistinguishable, to the user, from having arrived
  // just after it.
  const clear = useCallback(() => setBuffer(EMPTY), [])

  const status = !enabled || !groupIdentifier ? "idle" : died ? "error" : "live"
  return { events: buffer.events, overflowed: buffer.overflowed, status, clear }
}
