import { stableRowKey } from "./stable-row-key"

describe("stableRowKey", () => {
  it("hands the same object the same key on every ask", () => {
    const row = { message: "hello" }
    expect(stableRowKey(row)).toBe(stableRowKey(row))
  })

  it("hands distinct objects distinct keys, identical content or not", () => {
    // Two rows a producer really did emit twice must not collapse into one
    // React key — that is the promise content-derived keys cannot make.
    const a = { message: "dup" }
    const b = { message: "dup" }
    expect(stableRowKey(a)).not.toBe(stableRowKey(b))
  })

  it("keeps keys stable across interleaved asks", () => {
    const rows = Array.from({ length: 5 }, (_, i) => ({ i }))
    const first = rows.map(stableRowKey)
    // A sort flip re-asks in a different order; every row keeps its key.
    const second = [...rows].reverse().map(stableRowKey).reverse()
    expect(second).toEqual(first)
  })
})
