/**
 * keyed-throttle — a leading-plus-trailing-edge rate limiter with one
 * independent lane per string key.
 *
 * Built for the event stream's query invalidations (use-event-stream.ts):
 * during a Lambda burst the same query keys arrive in every flush window, and
 * invalidating an actively-observed query makes React Query refetch it — so
 * an unthrottled pass refetches instance lists and the topology graph many
 * times a second, saturating the browser's six-connections-per-origin pool
 * that navigations also need. Nothing here is invalidation-specific, though;
 * the class throttles arbitrary thunks.
 *
 * Semantics per key:
 *  - **Leading edge.** A key that has not run within the interval runs
 *    immediately, so sparse events stay exactly as fresh as an unthrottled
 *    call.
 *  - **Trailing edge.** Calls landing inside the interval are coalesced into
 *    one deferred run of the *latest* thunk when the interval expires, so the
 *    final state of a burst is never missed — only the redundant middle is
 *    dropped.
 */

export class KeyedThrottle {
  readonly #intervalMs: number
  /** Epoch ms at which each key last ran (leading or trailing). */
  readonly #lastRun = new Map<string, number>()
  /** Deferred trailing runs; `fn` is overwritten by later calls in the window. */
  readonly #trailing = new Map<string, { fn: () => void; timer: ReturnType<typeof setTimeout> }>()

  constructor(intervalMs: number) {
    this.#intervalMs = intervalMs
  }

  /**
   * Run `fn` for `key` now, or — if the key already ran within the interval —
   * defer it to the end of the interval, replacing any thunk already waiting
   * there.
   */
  run(key: string, fn: () => void): void {
    const pending = this.#trailing.get(key)
    if (pending) {
      pending.fn = fn
      return
    }

    const now = Date.now()
    const last = this.#lastRun.get(key)
    if (last === undefined || now - last >= this.#intervalMs) {
      this.#lastRun.set(key, now)
      fn()
      return
    }

    const entry = {
      fn,
      timer: setTimeout(
        () => {
          this.#trailing.delete(key)
          this.#lastRun.set(key, Date.now())
          entry.fn()
        },
        this.#intervalMs - (now - last),
      ),
    }
    this.#trailing.set(key, entry)
  }

  /** Drop every deferred trailing run. For teardown — nothing fires after this. */
  cancel(): void {
    for (const { timer } of this.#trailing.values()) {
      clearTimeout(timer)
    }
    this.#trailing.clear()
  }
}
