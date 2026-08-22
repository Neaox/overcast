/**
 * reconnecting-event-source — a small wrapper around `EventSource` that
 * reconnects with backoff and reports its own connection state.
 *
 * `EventSource` already retries a dropped connection on its own, but it
 * never says *when* the next attempt lands, and — the actual bug this fixes
 * (issue #1184) — nothing in the dashboard ever heard about the drop at
 * all: `use-event-stream.ts` had no `onerror`/`onopen` handling, so the
 * matrix kept showing whatever it last had, silently, with no way to tell a
 * stale view from a live one.
 *
 * Mirrors the pattern `web/src/workers/event-stream.connection.ts` already
 * uses for the main Overcast UI (built in #369/#382), scaled down: no shared
 * worker, no resume-point query param — the compat server's `/events`
 * replays its whole in-memory buffer to every new connection (see
 * `compat/AGENTS.md` § Compat server contract), so a fresh connection is
 * always caught up on its own. What is still needed, and was missing, is
 * *acting* on the drop: closing the stale source, scheduling the next
 * attempt, and telling the caller so the UI can re-seed from `/results`
 * (which reconnect() below signals via `onOpen`'s `priorAttempt`) and show a
 * status pill instead of quietly going stale.
 */

// ─── Backoff ────────────────────────────────────────────────────────────────

const BASE_DELAY_MS = 1_000;
const MAX_DELAY_MS = 5_000;

/**
 * Delay before the attempt that follows `failures` consecutive failures:
 * 1s, 2s, 4s, then 5s forever.
 *
 * The ceiling is low on purpose — this is a local emulator a developer
 * restarts by hand, so a long backoff just means staring longer at a
 * dashboard that has not noticed the server came back.
 */
export function retryDelayMs(failures: number): number {
  return Math.min(BASE_DELAY_MS * 2 ** failures, MAX_DELAY_MS);
}

// ─── EventSource seam ───────────────────────────────────────────────────────

/**
 * The slice of `EventSource` this module drives. jsdom has no `EventSource`,
 * so tests supply their own fake; a real `EventSource` satisfies this
 * structurally.
 */
export interface EventSourceLike {
  onopen: ((event: Event) => void) | null;
  onerror: ((event: Event) => void) | null;
  onmessage: ((event: MessageEvent<string>) => void) | null;
  close(): void;
}

export type ConnectionStatus = "connecting" | "open" | "reconnecting";

export interface ReconnectingEventSourceOptions {
  /** Stream URL. */
  url: string;
  /** Called for every message's raw `data` string, in arrival order. */
  onMessage: (data: string) => void;
  /** Called on every status transition, including the very first attempt. */
  onStatusChange: (status: ConnectionStatus, attempt: number) => void;
  /**
   * Called every time the connection opens successfully, including the
   * first time. `priorAttempt` is how many reconnect attempts preceded this
   * open — 0 for the initial connection, >0 for a recovered drop. Callers
   * use this to know when to re-seed state from a REST snapshot rather than
   * trusting the stream alone.
   */
  onOpen: (priorAttempt: number) => void;
  /** Opens the underlying connection. Overridden in tests. */
  open?: (url: string) => EventSourceLike;
}

// ─── ReconnectingEventSource ────────────────────────────────────────────────

export class ReconnectingEventSource {
  readonly #url: string;
  readonly #onMessage: (data: string) => void;
  readonly #onStatusChange: (status: ConnectionStatus, attempt: number) => void;
  readonly #onOpen: (priorAttempt: number) => void;
  readonly #open: (url: string) => EventSourceLike;

  #source: EventSourceLike | null = null;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #attempt = 0;
  #closed = false;

  constructor({
    url,
    onMessage,
    onStatusChange,
    onOpen,
    open,
  }: ReconnectingEventSourceOptions) {
    this.#url = url;
    this.#onMessage = onMessage;
    this.#onStatusChange = onStatusChange;
    this.#onOpen = onOpen;
    this.#open = open ?? ((target) => new EventSource(target));
    this.#connect();
  }

  /** Closes the connection and cancels any scheduled reconnect, permanently. */
  close(): void {
    this.#closed = true;
    this.#clearTimer();
    this.#source?.close();
    this.#source = null;
  }

  // ── Internals ─────────────────────────────────────────────────────────────

  #connect(): void {
    this.#clearTimer();
    this.#source?.close();

    this.#onStatusChange(
      this.#attempt === 0 ? "connecting" : "reconnecting",
      this.#attempt,
    );

    const source = this.#open(this.#url);
    this.#source = source;
    const isCurrent = () => source === this.#source;

    source.onopen = () => {
      if (!isCurrent()) return;
      const priorAttempt = this.#attempt;
      this.#attempt = 0;
      this.#onStatusChange("open", 0);
      this.#onOpen(priorAttempt);
    };

    source.onerror = () => {
      if (!isCurrent()) return;
      source.close();
      this.#source = null;
      if (this.#closed) return;
      this.#scheduleRetry();
    };

    source.onmessage = (event) => {
      if (!isCurrent()) return;
      this.#onMessage(event.data);
    };
  }

  #scheduleRetry(): void {
    const delay = retryDelayMs(this.#attempt);
    this.#attempt += 1;
    this.#onStatusChange("reconnecting", this.#attempt);
    this.#timer = setTimeout(() => {
      this.#timer = null;
      this.#connect();
    }, delay);
  }

  #clearTimer(): void {
    if (this.#timer === null) return;
    clearTimeout(this.#timer);
    this.#timer = null;
  }
}
