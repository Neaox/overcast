import { decodeBase64Text } from "@/lib/base64"

/** What the emulator sends: the bytes of `text`, base64-encoded. */
function encode(text: string): string {
  const bytes = new TextEncoder().encode(text)
  return btoa(String.fromCharCode(...bytes))
}

describe("decodeBase64Text", () => {
  it("decodes an ASCII log tail", () => {
    expect(decodeBase64Text(encode("START RequestId: 1234\n"))).toBe("START RequestId: 1234\n")
  })

  it.each([
    ["an accented character a Python handler printed", "café au lait"],
    ["an emoji a Node console.log printed", "done ✅"],
    ["a non-Latin script", "こんにちは"],
  ])("decodes %s rather than mangling it into mojibake", (_case, text) => {
    expect(decodeBase64Text(encode(text))).toBe(text)
  })

  it("returns an empty string for empty input", () => {
    expect(decodeBase64Text("")).toBe("")
  })

  it("returns null for a value outside the base64 alphabet rather than throwing", () => {
    expect(decodeBase64Text("not base64 at all!")).toBeNull()
  })

  it("returns null for a length no base64 string can have", () => {
    expect(decodeBase64Text("aGVsb")).toBeNull()
  })

  it("replaces a byte sequence that is not valid UTF-8 instead of failing the whole value", () => {
    // `hi`, an unpaired continuation byte, `there` — one bad byte in the middle.
    const bytes = new Uint8Array([0x68, 0x69, 0xff, 0x74, 0x68, 0x65, 0x72, 0x65])
    const encoded = btoa(String.fromCharCode(...bytes))
    expect(decodeBase64Text(encoded)).toBe("hi�there") // encoding-check:allow
  })
})
