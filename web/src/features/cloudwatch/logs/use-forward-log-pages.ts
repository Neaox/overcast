/**
 * Forward paging for a GetLogEvents-backed viewer whose live tail has died.
 *
 * A viewer like the log peek pages backward for history and leans on its live
 * tail for everything newer — so when the tail session dies (emulator restart,
 * dropped network, a connection gone quiet), events written after the death
 * are unreachable. GetLogEvents is natively bidirectional: the first fetched
 * page already carries a `nextForwardToken`, and this hook walks it.
 *
 * Design notes, deliberate:
 *
 * - The forward walk lives *outside* the backward infinite query rather than
 *   as `getPreviousPageParam`. React Query's bidirectional cache latches "no
 *   previous page" permanently once the end-token comes back, and every probe
 *   would deposit an empty page object in the cache; a separately-tracked
 *   token keeps the query's backward semantics untouched and the probe cheap.
 * - AWS's token contract is the stop signal: `nextForwardToken` is *always*
 *   returned, and receiving the SAME token you passed — not an empty events
 *   array — is the canonical "you are at the end".
 * - `exhausted` latches (also on a failed fetch, so a down emulator is probed
 *   once, not hammered). One drain per tail death: the red indicator already
 *   says the session is gone, and reopening the tail is the recovery path.
 * - Single-flight via a ref; per-fetch state writes happen only when a page
 *   starts/finishes — there is no interval and no polling, so an idle dead
 *   tail costs nothing.
 */

import { useCallback, useRef, useState } from "react"
import { logs } from "@/services/api"
import type { LogEvent } from "@/types"

export interface ForwardLogPages {
  /** Everything fetched forward so far, oldest first, strictly newer than the query's first page. */
  events: LogEvent[]
  /** True once the walk hit the end of the stream (or a failed fetch). */
  exhausted: boolean
  /** A page fetch is in flight. */
  loading: boolean
  /** Fetch the next newer page. Single-flight; a no-op without a start token. */
  loadNewer: () => void
}

interface ForwardState {
  events: LogEvent[]
  exhausted: boolean
  loading: boolean
}

const EMPTY: ForwardState = { events: [], exhausted: false, loading: false }

/** A walk's state together with the stream it belongs to. */
interface Snapshot {
  identity: string
  state: ForwardState
}

/** Where the walk has got to. Only ever touched from callbacks, never in render. */
interface Walk {
  identity: string
  /** The token to fetch next; undefined until the first fetch chains off startToken. */
  token: string | undefined
  inFlight: boolean
  /** Bumped when the stream identity changes, so a stale in-flight fetch discards itself. */
  epoch: number
}

export function useForwardLogPages({
  logGroup,
  logStream,
  startToken,
}: {
  logGroup: string | undefined
  logStream: string | undefined
  /** The newest fetched page's nextForwardToken — where "newer than loaded" starts. */
  startToken: string | undefined
}): ForwardLogPages {
  // A walk belongs to one stream, and carrying that identity in the snapshot is
  // what makes the reset a *derivation*: a snapshot stamped with a stream the
  // viewer has moved off is simply not this stream's state. Writing EMPTY back
  // from an effect would say the same thing at the cost of an extra render every
  // time the viewer changes stream.
  const identity = JSON.stringify([logGroup, logStream])
  const [snapshot, setSnapshot] = useState<Snapshot>({ identity, state: EMPTY })
  const state = snapshot.identity === identity ? snapshot.state : EMPTY

  const walkRef = useRef<Walk>({ identity, token: undefined, inFlight: false, epoch: 0 })

  const loadNewer = useCallback(() => {
    const walk = walkRef.current
    if (walk.identity !== identity) {
      // A new stream starts a new epoch: the walk restarts from the new
      // stream's start token, and anything still in flight from the old stream
      // discards itself when it lands. Doing this here rather than in an effect
      // keeps every ref write inside a callback.
      walk.identity = identity
      walk.token = undefined
      walk.inFlight = false
      walk.epoch++
    }
    if (walk.inFlight || !logGroup || !logStream) return
    const token = walk.token ?? startToken
    if (!token) return
    walk.inFlight = true
    const epoch = walk.epoch

    /** Fold into the snapshot, starting fresh if the last one was another stream's. */
    const update = (fn: (prev: ForwardState) => ForwardState) => {
      setSnapshot((prev) => ({
        identity,
        state: fn(prev.identity === identity ? prev.state : EMPTY),
      }))
    }

    update((prev) => ({ ...prev, loading: true }))
    void (async () => {
      try {
        const page = await logs.getEvents(logGroup, logStream, { nextToken: token, limit: 200 })
        if (walkRef.current.epoch !== epoch) return
        const next = page.nextForwardToken
        // The same token coming back is AWS's end-of-stream signal.
        const atEnd = !next || next === token
        if (next) walkRef.current.token = next
        update((prev) => ({
          events: page.events.length ? [...prev.events, ...page.events] : prev.events,
          exhausted: atEnd,
          loading: false,
        }))
      } catch (err) {
        if (walkRef.current.epoch !== epoch) return
        // Latch rather than retry: if the emulator is down (the usual reason a
        // tail died), spinning against it helps nobody. A fresh tail session
        // resets everything.
        console.warn("forward log paging stopped after a failed fetch", err)
        update((prev) => ({ ...prev, exhausted: true, loading: false }))
      } finally {
        if (walkRef.current.epoch === epoch) walkRef.current.inFlight = false
      }
    })()
  }, [identity, logGroup, logStream, startToken])

  return { ...state, loadNewer }
}
