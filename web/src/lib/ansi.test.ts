import { isPlain, parseAnsi, stripAnsi } from "./ansi"

const ESC = String.fromCharCode(27)
const BEL = String.fromCharCode(7)

/* The line that started this: a Bitnami WordPress container's entrypoint, as
   the awslogs driver stores it. Before the viewer understood SGR, every one of
   these rendered as `[38;5;6mwordpress [38;5;5m21:33:36.72 [0m…`. */
const BITNAMI = `${ESC}[38;5;6mwordpress ${ESC}[38;5;5m21:33:37.24 ${ESC}[0m${ESC}[38;5;1mERROR${ESC}[0m ==> WORDPRESS_PASSWORD must be set`

describe("stripAnsi", () => {
  it("leaves a message with nothing to strip untouched, and identical", () => {
    const plain = "Certificate request self-signature ok"
    expect(stripAnsi(plain)).toBe(plain)
  })

  it("removes the colour sequences from a real container log line", () => {
    expect(stripAnsi(BITNAMI)).toBe(
      "wordpress 21:33:37.24 ERROR ==> WORDPRESS_PASSWORD must be set",
    )
  })

  it("leaves no bracket litter behind — the symptom the escape byte hides", () => {
    expect(stripAnsi(BITNAMI)).not.toContain("[38;5")
    expect(stripAnsi(BITNAMI)).not.toContain("[0m")
  })

  it("drops sequences a log viewer cannot act on: cursor moves, erases, titles", () => {
    expect(stripAnsi(`a${ESC}[2Kb${ESC}[1;5Hc`)).toBe("abc")
    expect(stripAnsi(`${ESC}]0;a window title${BEL}done`)).toBe("done")
  })

  it("drops stray control bytes but keeps tabs and newlines", () => {
    expect(stripAnsi(`a${String.fromCharCode(1)}b${String.fromCharCode(127)}c`)).toBe("abc")
    expect(stripAnsi("progress\rdone")).toBe("progressdone")
    expect(stripAnsi("col\tumn\nnext")).toBe("col\tumn\nnext")
  })

  it("survives a sequence cut off by the end of the message", () => {
    expect(stripAnsi(`done${ESC}[38;5`)).toBe("done")
    expect(stripAnsi(`done${ESC}`)).toBe("done")
  })
})

describe("parseAnsi", () => {
  it("returns one plain segment for text with no sequences", () => {
    const segments = parseAnsi("realpath: /bitnami/apache/conf: No such file or directory")
    expect(segments).toHaveLength(1)
    expect(isPlain(segments[0].style)).toBe(true)
    expect(segments[0].text).toBe("realpath: /bitnami/apache/conf: No such file or directory")
  })

  it("returns one empty segment for an empty message", () => {
    expect(parseAnsi("")).toEqual([{ text: "", style: {} }])
  })

  it("splits a container line into the runs its colours describe", () => {
    const segments = parseAnsi(BITNAMI)
    expect(segments.map((s) => s.text)).toEqual([
      "wordpress ",
      "21:33:37.24 ",
      "ERROR",
      " ==> WORDPRESS_PASSWORD must be set",
    ])
    // 38;5;6 is cyan, 38;5;1 red — mapped onto the theme ramp, not raw sRGB.
    expect(segments[0].style.fg).toBe("var(--cat-6)")
    expect(segments[2].style.fg).toBe("var(--cat-1)")
    expect(isPlain(segments[3].style)).toBe(true)
  })

  it("concatenates back to the stripped text, losing no characters", () => {
    expect(
      parseAnsi(BITNAMI)
        .map((s) => s.text)
        .join(""),
    ).toBe(stripAnsi(BITNAMI))
  })

  it("reads the basic and bright colour codes", () => {
    expect(parseAnsi(`${ESC}[31mred`)[0].style.fg).toBe("var(--cat-1)")
    expect(parseAnsi(`${ESC}[92mgreen`)[0].style.fg).toBe("var(--cat-4)")
  })

  it("reads 24-bit colour, mapped to the nearest ramp hue", () => {
    expect(parseAnsi(`${ESC}[38;2;220;30;30mred`)[0].style.fg).toBe("var(--cat-1)")
    expect(parseAnsi(`${ESC}[38;2;40;90;220mblue`)[0].style.fg).toBe("var(--cat-7)")
  })

  it("maps a greyscale palette index to a foreground weight, not a hue", () => {
    expect(parseAnsi(`${ESC}[38;5;236mdim`)[0].style.fg).toBe("var(--fg-subtle)")
    expect(parseAnsi(`${ESC}[38;5;252mlight`)[0].style.fg).toBe("var(--fg)")
  })

  it("softens a background so the theme's surface still shows through", () => {
    expect(parseAnsi(`${ESC}[41mred bg`)[0].style.bg).toBe(
      "color-mix(in srgb, var(--cat-1) 22%, transparent)",
    )
  })

  it("carries weight, emphasis and faintness", () => {
    const [segment] = parseAnsi(`${ESC}[1;3;4;9;2mall of it`)
    expect(segment.style).toMatchObject({
      bold: true,
      italic: true,
      underline: true,
      strike: true,
      dim: true,
    })
  })

  it("turns attributes back off again", () => {
    const segments = parseAnsi(`${ESC}[1mbold${ESC}[22mnormal`)
    expect(segments[0].style.bold).toBe(true)
    expect(isPlain(segments[1].style)).toBe(true)
  })

  it("treats a bare ESC[m and ESC[0m alike, as a reset", () => {
    expect(isPlain(parseAnsi(`${ESC}[31ma${ESC}[mb`)[1].style)).toBe(true)
    expect(isPlain(parseAnsi(`${ESC}[31ma${ESC}[0mb`)[1].style)).toBe(true)
  })

  it("swaps foreground and background for reverse video", () => {
    const [segment] = parseAnsi(`${ESC}[7;31minverted`)
    expect(segment.style.fg).toBe("var(--fg)")
    expect(segment.style.bg).toBe("color-mix(in srgb, var(--cat-1) 22%, transparent)")
  })

  it("does not start a new run when a sequence changes nothing visible", () => {
    // Reset then re-set the same colour: two sequences, one run.
    const segments = parseAnsi(`${ESC}[31ma${ESC}[0m${ESC}[31mb`)
    expect(segments).toHaveLength(1)
    expect(segments[0].text).toBe("ab")
  })

  it("drops non-SGR sequences without breaking the run around them", () => {
    const segments = parseAnsi(`${ESC}[31mred${ESC}[2K still red`)
    expect(segments).toHaveLength(1)
    expect(segments[0].text).toBe("red still red")
    expect(segments[0].style.fg).toBe("var(--cat-1)")
  })

  it("ignores a truncated extended-colour sequence rather than printing it", () => {
    const segments = parseAnsi(`${ESC}[38;5mtext`)
    expect(segments.map((s) => s.text).join("")).toBe("text")
  })

  it("is not confused by a second call — the global regex keeps no state", () => {
    expect(parseAnsi(BITNAMI)).toEqual(parseAnsi(BITNAMI))
    expect(stripAnsi(BITNAMI)).toBe(stripAnsi(BITNAMI))
  })
})

/*
 * A log event is not a terminal line. Containers routinely emit a colour and
 * never reset it — the next `write` is a separate CloudWatch event, and the
 * viewer renders events in whatever order the virtualiser mounts them, so any
 * state carried between events would show up as colour attaching to the wrong
 * row and changing on scroll. Every message therefore starts from a clean SGR
 * state, and every malformed sequence is contained to the event it appears in.
 */
describe("parseAnsi > one event cannot affect the next", () => {
  const UNTERMINATED = `${ESC}[31mstarted red and never reset`

  it("does not carry a colour out of a message that never reset it", () => {
    parseAnsi(UNTERMINATED)
    const after = parseAnsi("an ordinary line")
    expect(after).toHaveLength(1)
    expect(isPlain(after[0].style)).toBe(true)
  })

  it("gives the same answer whichever order the messages are parsed in", () => {
    const plainFirst = [parseAnsi("an ordinary line"), parseAnsi(UNTERMINATED)]
    const plainSecond = [parseAnsi(UNTERMINATED), parseAnsi("an ordinary line")]
    expect(plainFirst[0]).toEqual(plainSecond[1])
    expect(plainFirst[1]).toEqual(plainSecond[0])
  })

  it.each([
    ["truncated CSI", `red ${ESC}[38;5`],
    ["lone escape", `red ${ESC}`],
    ["unterminated OSC", `red ${ESC}]8;;https://example.com`],
    ["parameters with no final byte", `red ${ESC}[1;2;3`],
    ["a final byte with no introducer", `red 38;5;1m`],
    ["nothing but escapes", `${ESC}${ESC}[${ESC}]`],
  ])("contains a %s to its own message", (_name, message) => {
    parseAnsi(message)
    expect(isPlain(parseAnsi("the next event")[0].style)).toBe(true)
    expect(stripAnsi("the next event")).toBe("the next event")
  })

  it("swallows an unterminated OSC to the end of that message only", () => {
    // A terminal reads an OSC until BEL or ST; with neither, the rest of the
    // message is the (never-ended) title string. The loss stops at the event
    // boundary, which is why this is safe to be faithful about.
    expect(stripAnsi(`keep ${ESC}]0;lost title`)).toBe("keep ")
    expect(stripAnsi(`keep ${ESC}]0;ended${BEL} kept`)).toBe("keep  kept")
  })
})

describe("parseAnsi > malformed input", () => {
  it("never throws, and never loses a printable character, on random junk", () => {
    // A deterministic LCG rather than Math.random: a failure has to be
    // reproducible from the test alone.
    let seed = 0x2f6e2b1
    const next = () => (seed = (seed * 1103515245 + 12345) & 0x7fffffff)
    const alphabet = [ESC, BEL, "[", "]", ";", ":", "?", "m", "K", "0", "3", "8", "a", " ", "\n"]

    for (let n = 0; n < 500; n++) {
      let message = ""
      for (let i = 0; i < 40; i++) message += alphabet[next() % alphabet.length]

      const segments = parseAnsi(message)
      expect(segments.length).toBeGreaterThan(0)
      // The two entry points must agree: whatever the runs say, the stripped
      // message is the same text.
      expect(segments.map((s) => s.text).join("")).toBe(stripAnsi(message))
      // And nothing invisible survives into the DOM.
      expect(stripAnsi(message)).not.toContain(ESC)
      expect(stripAnsi(message)).not.toContain(BEL)
    }
  })

  it("shrugs off parameters that are not numbers", () => {
    expect(() => parseAnsi(`${ESC}[38;2;x;y;zmtext`)).not.toThrow()
    expect(() => parseAnsi(`${ESC}[999999999999mtext`)).not.toThrow()
    expect(() => parseAnsi(`${ESC}[;;;;;mtext`)).not.toThrow()
  })

  it("terminates on a long message with no sequence in it", () => {
    const long = "x".repeat(200_000)
    expect(stripAnsi(long)).toBe(long)
    expect(parseAnsi(long)).toHaveLength(1)
  })
})
