import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { KeyedThrottle } from "./keyed-throttle"

const INTERVAL = 1_000

describe("KeyedThrottle", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it("runs immediately when the key has not run recently", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const fn = vi.fn()

    throttle.run("a", fn)

    expect(fn).toHaveBeenCalledTimes(1)
  })

  it("suppresses repeat calls within the interval, then runs the trailing call", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const fn = vi.fn()

    throttle.run("a", fn)
    vi.advanceTimersByTime(100)
    throttle.run("a", fn)
    vi.advanceTimersByTime(100)
    throttle.run("a", fn)
    expect(fn).toHaveBeenCalledTimes(1)

    vi.advanceTimersByTime(INTERVAL)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it("the trailing call runs the latest function, not the first suppressed one", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const first = vi.fn()
    const stale = vi.fn()
    const latest = vi.fn()

    throttle.run("a", first)
    vi.advanceTimersByTime(100)
    throttle.run("a", stale)
    vi.advanceTimersByTime(100)
    throttle.run("a", latest)

    vi.advanceTimersByTime(INTERVAL)
    expect(stale).not.toHaveBeenCalled()
    expect(latest).toHaveBeenCalledTimes(1)
  })

  it("never lets a key run more than once per interval during a sustained burst", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const fn = vi.fn()

    // Ten seconds of a call every 50 ms.
    for (let t = 0; t < 10_000; t += 50) {
      throttle.run("a", fn)
      vi.advanceTimersByTime(50)
    }

    expect(fn.mock.calls.length).toBeLessThanOrEqual(1 + 10_000 / INTERVAL)
    expect(fn.mock.calls.length).toBeGreaterThan(1)
  })

  it("throttles each key independently", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const a = vi.fn()
    const b = vi.fn()

    throttle.run("a", a)
    vi.advanceTimersByTime(100)
    throttle.run("b", b)

    expect(a).toHaveBeenCalledTimes(1)
    expect(b).toHaveBeenCalledTimes(1)
  })

  it("returns to leading-edge behaviour after a quiet interval", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const fn = vi.fn()

    throttle.run("a", fn)
    vi.advanceTimersByTime(INTERVAL)
    throttle.run("a", fn)

    expect(fn).toHaveBeenCalledTimes(2)
  })

  it("cancel drops every scheduled trailing call", () => {
    const throttle = new KeyedThrottle(INTERVAL)
    const fn = vi.fn()

    throttle.run("a", fn)
    vi.advanceTimersByTime(100)
    throttle.run("a", fn)
    throttle.run("b", fn)
    throttle.cancel()

    vi.advanceTimersByTime(INTERVAL * 2)
    // Only the leading-edge calls ran ("a" then cold "b"); "a"'s trailing call was cancelled.
    expect(fn).toHaveBeenCalledTimes(2)
  })
})
