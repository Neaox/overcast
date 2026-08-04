import { isNetworkError, readsAsNetworkFailure } from "@/lib/network-error"

describe("isNetworkError", () => {
  it.each([
    ["Failed to fetch", "Chrome"],
    ["NetworkError when attempting to fetch resource.", "Firefox"],
    ["Load failed", "Safari"],
  ])("recognises %s, the wording %s uses for a request that never landed", (message) => {
    expect(isNetworkError(new Error(message))).toBe(true)
  })

  it("recognises an error typed as a network error whatever its message says", () => {
    const error = new Error("aborted")
    error.name = "NetworkError"
    expect(isNetworkError(error)).toBe(true)
  })

  it("leaves a service error alone — it reached a server and got an answer", () => {
    expect(isNetworkError(new Error("The specified bucket is not empty."))).toBe(false)
  })

  it("does not read a failed upload as a dropped connection", () => {
    expect(isNetworkError(new Error("Upload failed · 501"))).toBe(false)
  })

  it("is false for anything that is not an Error", () => {
    expect(isNetworkError("Failed to fetch")).toBe(false)
    expect(isNetworkError(undefined)).toBe(false)
  })
})

describe("readsAsNetworkFailure", () => {
  it("matches the copy a failure toast carries when the connection dropped", () => {
    expect(readsAsNetworkFailure("Network error")).toBe(true)
    expect(readsAsNetworkFailure("Failed to fetch")).toBe(true)
    expect(readsAsNetworkFailure("Load failed")).toBe(true)
  })

  it("does not match a failure the emulator actually answered", () => {
    expect(readsAsNetworkFailure("The specified bucket is not empty.")).toBe(false)
    expect(readsAsNetworkFailure("Upload failed · 501")).toBe(false)
  })
})
