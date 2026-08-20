/**
 * The persistent-worker client kernel: one implementation of the
 * lazy-singleton Worker + request-id correlation + pending-map shape that
 * three modules used to hand-roll independently (syntax highlighting, docs
 * search, map layout) — each with a guard the others lacked. The kernel owns
 * the machinery; the call sites keep their distinct semantics as parameters.
 *
 * What the kernel owns:
 *
 * - **Lazy construction.** The caller passes a factory, so the
 *   `new Worker(new URL("./x.worker.ts", import.meta.url), { type: "module" })`
 *   literal stays at the call site — Vite's worker bundling requires the
 *   static URL expression there. Construction is deferred to the first
 *   request; a factory that throws (CSP, no `Worker` global in jsdom/SSR)
 *   retires the client for the session and every request answers through its
 *   synchronous fallback.
 * - **Correlation.** A monotonic request id stamped into each outgoing
 *   message by the caller's `message(id)` builder; replies correlate on
 *   `reply.id`. A reply for an unknown id — already settled, cancelled, or
 *   from a retired worker generation — is dropped.
 * - **Pending-map hygiene.** Every path out of a request — reply, worker
 *   error, failed post, abort — removes its pending entry. Nothing leaks and
 *   nothing settles twice.
 * - **Failure recovery.** `onerror`/`onmessageerror` terminates the worker
 *   and resolves everything in flight *synchronously* through each request's
 *   `fallback` — no caller is ever left hanging, and no promise from the
 *   kernel ever rejects. Up to `failureLimit` failures the next request
 *   respawns via the factory; at the limit the client gives up for the
 *   session (a worker that keeps dying is broken in a way retrying won't
 *   fix) and requests answer synchronously from then on.
 * - **Guarded postMessage.** A terminated or unusable worker throws
 *   synchronously from `postMessage`; the kernel unwinds that request's
 *   bookkeeping and hands back the fallback value, synchronously.
 *
 * What each caller keeps: its message/reply wire types (`Reply` generic,
 * per-request `decode`), its fallback semantics, transfer lists as a
 * per-request option, cancellation (`signal` + `cancelMessage` — the docs
 * search shape), and stale-reply tolerance (the map layout shape) — a caller
 * that only wants the latest result compares its own generation counter when
 * the promise resolves; the kernel resolves every reply it can correlate.
 */

/** Configuration for one worker client (one worker, one protocol). */
export interface WorkerClientOptions {
  /**
   * Constructs the Worker. Must contain the static
   * `new URL("...", import.meta.url)` literal so Vite can bundle the worker.
   * Called lazily on first use, and again after a tolerated failure.
   */
  create: () => Worker
  /**
   * How many worker failures (uncaught errors, undeliverable messages) the
   * client tolerates before giving up on off-thread work for the session.
   * Each failure below the limit terminates the broken worker and lets the
   * next request respawn a fresh one. Defaults to 1 — fail once, give up.
   */
  failureLimit?: number
}

/** Per-request parameters: the caller's semantics, not the kernel's. */
export interface WorkerRequest<Reply, T> {
  /** Builds the outgoing message with the kernel-assigned correlation id. */
  message: (id: number) => unknown
  /** Transferables to move (not clone) with the outgoing message. */
  transfer?: Transferable[]
  /** Maps a correlated reply to the caller's result type. */
  decode: (reply: Reply) => T
  /**
   * Synchronous answer used when the worker cannot: construction failed,
   * postMessage threw, the worker errored while this request was in flight,
   * or the request was aborted. Must not throw.
   */
  fallback: () => T
  /** Optional cancellation. Aborting resolves the request with `fallback()`. */
  signal?: AbortSignal
  /**
   * Message posted to the worker when `signal` aborts, so it can stop the
   * work server-side (e.g. `{ type: "cancel", id }`). Only meaningful with
   * `signal`.
   */
  cancelMessage?: (id: number) => unknown
}

/**
 * A request's outcome: `async` when the work is genuinely on the worker,
 * `sync` when the kernel answered through the fallback without a round-trip.
 * The split is part of the contract — callers like the highlight facade
 * return a plain value on the sync path so consumers can apply it in the
 * same pass. The promise on the async path NEVER rejects.
 */
export type WorkerRequestOutcome<T> =
  | { readonly async: true; readonly promise: Promise<T> }
  | { readonly async: false; readonly value: T }

export interface WorkerClient<Reply extends { id: number }> {
  /**
   * Sends one correlated request. Returns a sync outcome (the fallback's
   * value) when no worker is available or the post fails; otherwise an async
   * outcome whose promise resolves with the decoded reply — or with the
   * fallback, should the worker die first. It never rejects.
   */
  request<T>(req: WorkerRequest<Reply, T>): WorkerRequestOutcome<T>
}

interface PendingEntry {
  /** Settles the caller's promise (with cleanup); value is decode's or fallback's. */
  settle: (value: unknown) => void
  decode: (reply: never) => unknown
  fallback: () => unknown
}

/** One persistent-worker client. See the module doc for the division of labor. */
export function createWorkerClient<Reply extends { id: number }>(
  options: WorkerClientOptions,
): WorkerClient<Reply> {
  const failureLimit = options.failureLimit ?? 1
  // undefined = may (re)spawn; null = unavailable or given up for the session.
  let worker: Worker | null | undefined
  let failures = 0
  let nextId = 1
  const pending = new Map<number, PendingEntry>()

  // Any failure shape — an uncaught error in the worker, an undeliverable
  // message either way — retires this worker and resolves whatever was
  // waiting on it synchronously via each request's fallback: nothing may be
  // left hanging, on any path.
  const retire = (created: Worker) => {
    failures++
    worker = failures >= failureLimit ? null : undefined
    created.terminate()
    const stranded = [...pending.values()]
    pending.clear()
    for (const entry of stranded) entry.settle(entry.fallback())
  }

  const ensureWorker = (): Worker | null => {
    if (worker !== undefined) return worker
    try {
      const created = options.create()
      created.onmessage = (event: MessageEvent<Reply>) => {
        const entry = pending.get(event.data.id)
        if (!entry) return
        pending.delete(event.data.id)
        entry.settle(entry.decode(event.data as never))
      }
      created.onerror = () => retire(created)
      created.onmessageerror = () => retire(created)
      worker = created
    } catch {
      // No Worker global, CSP refusal, bad URL: off-thread work is
      // unavailable this session. Not counted as a failure — there is
      // nothing to respawn.
      worker = null
    }
    return worker
  }

  function request<T>(req: WorkerRequest<Reply, T>): WorkerRequestOutcome<T> {
    const w = ensureWorker()
    if (w === null) return { async: false, value: req.fallback() }
    if (req.signal?.aborted) return { async: false, value: req.fallback() }

    const id = nextId++
    let resolve!: (value: T) => void
    const promise = new Promise<T>((r) => {
      resolve = r
    })

    const onAbort = () => {
      // The entry may already be gone (reply raced the abort); only a live
      // one gets cancelled — and cancellation must not double-settle.
      if (!pending.delete(id)) return
      if (req.cancelMessage) {
        try {
          w.postMessage(req.cancelMessage(id))
        } catch {
          // The worker died between the request and the abort; retire/post
          // guards elsewhere already answered or will answer the request.
        }
      }
      resolve(req.fallback())
    }
    const settle = (value: unknown) => {
      req.signal?.removeEventListener("abort", onAbort)
      resolve(value as T)
    }

    pending.set(id, { settle, decode: req.decode, fallback: req.fallback })
    req.signal?.addEventListener("abort", onAbort, { once: true })
    try {
      if (req.transfer) w.postMessage(req.message(id), req.transfer)
      else w.postMessage(req.message(id))
    } catch {
      // A terminated or unusable worker throws synchronously: unwind this
      // request's bookkeeping and answer on the calling thread instead.
      pending.delete(id)
      req.signal?.removeEventListener("abort", onAbort)
      return { async: false, value: req.fallback() }
    }
    return { async: true, promise }
  }

  return { request }
}
