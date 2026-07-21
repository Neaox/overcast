import { afterEach, describe, expect, it, vi } from "vitest"
import { createId } from "./id"

describe("createId", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it("uses native randomUUID when available", () => {
    vi.stubGlobal("crypto", {
      randomUUID: vi.fn(() => "native-id"),
    })

    expect(createId()).toBe("native-id")
  })

  it("falls back when randomUUID is unavailable", () => {
    vi.stubGlobal("crypto", {})
    vi.spyOn(Date, "now").mockReturnValue(12345)
    vi.spyOn(Math, "random").mockReturnValue(0.5)

    expect(createId()).toMatch(/^id-12345-[a-z0-9]+-[a-z0-9]+$/)
  })
})
