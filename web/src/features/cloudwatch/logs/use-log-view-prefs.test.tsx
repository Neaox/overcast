/**
 * The stream viewer's display toggles survive a revisit: one versioned
 * localStorage key, read once at mount, written once per change. Anything
 * corrupt or missing falls back to the defaults without a word — a preferences
 * store must never be the reason a log viewer fails to open.
 */
import { act, renderHook } from "@testing-library/react"
import { beforeEach, describe, expect, it } from "vitest"
import { LOG_VIEW_PREFS_KEY, defaultLogViewPrefs, useLogViewPrefs } from "./use-log-view-prefs"

beforeEach(() => {
  localStorage.clear()
})

describe("useLogViewPrefs", () => {
  it("starts from the defaults when nothing is stored", () => {
    const { result } = renderHook(() => useLogViewPrefs())

    expect(result.current.prefs).toEqual(defaultLogViewPrefs)
  })

  it("restores stored preferences on mount", () => {
    localStorage.setItem(
      LOG_VIEW_PREFS_KEY,
      JSON.stringify({ formatted: true, sortAsc: false, displayMode: "plain" }),
    )

    const { result } = renderHook(() => useLogViewPrefs())

    expect(result.current.prefs.formatted).toBe(true)
    expect(result.current.prefs.sortAsc).toBe(false)
    expect(result.current.prefs.displayMode).toBe("plain")
    // Unmentioned keys keep their defaults — a stored blob from an older
    // build never strips a newer toggle of its default.
    expect(result.current.prefs.wrapLines).toBe(defaultLogViewPrefs.wrapLines)
  })

  it("writes a change back under the versioned key", () => {
    const { result } = renderHook(() => useLogViewPrefs())

    act(() => result.current.setPref("utc", true))

    expect(result.current.prefs.utc).toBe(true)
    expect(JSON.parse(localStorage.getItem(LOG_VIEW_PREFS_KEY)!)).toMatchObject({ utc: true })
  })

  it("falls back to the defaults silently when the stored value is corrupt", () => {
    localStorage.setItem(LOG_VIEW_PREFS_KEY, "{definitely not json")

    const { result } = renderHook(() => useLogViewPrefs())

    expect(result.current.prefs).toEqual(defaultLogViewPrefs)
  })

  it("ignores stored values of the wrong type rather than trusting them", () => {
    localStorage.setItem(
      LOG_VIEW_PREFS_KEY,
      JSON.stringify({ formatted: "yes", displayMode: "sideways", wrapLines: 1 }),
    )

    const { result } = renderHook(() => useLogViewPrefs())

    expect(result.current.prefs).toEqual(defaultLogViewPrefs)
  })

  it("does not write to storage on mount, only on change", () => {
    renderHook(() => useLogViewPrefs())

    expect(localStorage.getItem(LOG_VIEW_PREFS_KEY)).toBeNull()
  })
})
