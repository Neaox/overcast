import { afterEach, beforeEach, vi } from "vitest"

/** The only member of the global `jest` that Testing Library ever calls. */
interface FakeTimerBridge {
  advanceTimersByTime: (ms: number) => void
}

const globals = globalThis as typeof globalThis & { jest?: FakeTimerBridge }

/**
 * Whether Vitest's fake clock is installed right now.
 *
 * Sniffed the same way Testing Library does it, off the property
 * `@sinonjs/fake-timers` hangs on the functions it replaces, so the two can
 * never disagree about which clock is running.
 */
function fakeTimersInstalled(): boolean {
  return Object.prototype.hasOwnProperty.call(setTimeout, "clock")
}

/**
 * Handed to `userEvent.setup` in `render.tsx`.
 *
 * user-event paces its pointer and keyboard sequences with real `setTimeout`
 * calls of its own, separate from anything Testing Library does, and awaits
 * them. Frozen, those never resolve. It offers this hook for exactly that
 * reason — it just needs to be a no-op when the clock is real, because
 * `vi.advanceTimersByTime` throws unless timers are mocked.
 */
export function advanceTimers(ms: number): void {
  if (fakeTimersInstalled()) vi.advanceTimersByTime(ms)
}

/**
 * Fake timers for the surrounding `describe`, wired so Testing Library can see
 * them. Use this instead of a bare `vi.useFakeTimers()` in any block that also
 * awaits `user.*` or `findBy*`.
 *
 * On its own, `vi.useFakeTimers()` deadlocks both. Testing Library decides
 * whether a fake clock is installed via `jestFakeTimersAreEnabled()`
 * (`@testing-library/dom/dist/helpers.js`), which returns `false` unless a
 * global `jest` exists — so under Vitest it always says "real timers" and two
 * call sites end up waiting on a frozen clock nothing advances:
 *
 *   - RTL's `asyncWrapper` drains the microtask queue through
 *     `setTimeout(…, 0)`, which is what hangs `await user.click(…)`.
 *   - `waitFor`'s polling loop, which is what hangs `findBy*` and `waitFor`.
 *
 * Both reach for `jest.advanceTimersByTime` and nothing else, so a one-method
 * bridge to `vi` is enough to unblock them.
 *
 * The bridge is installed per-block rather than in `setup.ts` so that suites
 * which never asked for it cannot start looking like Jest to library code that
 * sniffs for the global. It cannot misreport the clock either: the same
 * Testing Library helper goes on to check `setTimeout.clock`, which Vitest sets
 * only while its fake clock is actually installed.
 */
export function useFakeTimers(): void {
  beforeEach(() => {
    vi.useFakeTimers()
    globals.jest = { advanceTimersByTime: (ms) => vi.advanceTimersByTime(ms) }
  })

  afterEach(() => {
    delete globals.jest
    vi.useRealTimers()
  })
}
