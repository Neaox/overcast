/**
 * ANSI escape-sequence handling for log messages.
 *
 * Containers colour their own output — Bitnami's entrypoints, npm, pip, `go
 * test`, anything using a logging library with colour on by default — and the
 * `awslogs` driver forwards those bytes verbatim, so a CloudWatch log event
 * legitimately contains `ESC[38;5;2mINFO ESC[0m`. Real AWS stores them too, so
 * the emulator keeps the message byte-for-byte and the *viewer* is what has to
 * understand them: rendered as colour, rather than as `[38;5;2m` litter with an
 * invisible control character in front of it.
 *
 * Colours resolve to the theme's categorical ramp rather than to raw sRGB, so a
 * line coloured by the container stays legible on both the light and the dark
 * surface. Everything that is not an SGR (colour/weight) sequence — cursor
 * movement, erase-line, OSC title changes, stray control bytes — is dropped,
 * because a log viewer has no cursor for it to move.
 */

export interface AnsiStyle {
  /** CSS colour for the text, when the sequence set one. */
  fg?: string
  /** CSS colour behind the text, already softened to keep the text readable. */
  bg?: string
  bold?: boolean
  italic?: boolean
  underline?: boolean
  strike?: boolean
  /** Faint (SGR 2) — rendered as reduced opacity. */
  dim?: boolean
}

export interface AnsiSegment {
  text: string
  style: AnsiStyle
}

/** Shared object for unstyled runs, so callers can render them as bare text. */
const PLAIN: AnsiStyle = Object.freeze({})

/** True when a segment carries no styling and needs no wrapping element. */
export function isPlain(style: AnsiStyle): boolean {
  return style === PLAIN
}

/*
 * The scanner recognises, in order:
 *   1. CSI  — ESC [ params final-byte   (SGR when the final byte is `m`)
 *   2. OSC  — ESC ] ... BEL | ESC \     (window titles, hyperlinks)
 *   3. any other two-byte escape
 *   4. a lone control byte: C0 except tab/newline, DEL, and C1
 *
 * Tab and newline survive: a multi-line log event is still one event, and every
 * viewer renders it in a `white-space: pre-wrap` box.
 *
 * Built from strings rather than written as a literal so the source carries no
 * control characters of its own — a file that does is one careless editor away
 * from a corrupted pattern, and the mangling is invisible in review.
 */
const ESC = "\\u001b"
const CONTROL = "\\u0000-\\u0008\\u000b-\\u001f\\u007f-\\u009f"
const SEQUENCE = new RegExp(
  `${ESC}\\[([\\d;:?]*)([@-~])` + // CSI, parameters and final byte captured
    `|${ESC}\\[[\\d;:?]*$` + // CSI cut short by a truncated message
    `|${ESC}\\][\\s\\S]*?(?:\\u0007|${ESC}\\\\|$)` + // OSC up to BEL or ST
    `|${ESC}[@-Z\\\\-_]` + // any other escape
    `|[${CONTROL}]`, // a control byte on its own
  "g",
)
const HAS_SEQUENCE = new RegExp(`[${CONTROL}]`)

/**
 * The eight ANSI colours mapped onto the theme's categorical ramp, which is
 * defined per theme at a lightness that clears AA on every surface it is used
 * on. Bright variants (8–15) reuse the same hue: a second ramp would buy one
 * more shade of green at the cost of the ramp's contrast guarantee.
 */
const BASIC: readonly string[] = [
  "var(--fg-subtle)", // black — never literal black; it would vanish on the dark theme
  "var(--cat-1)", // red
  "var(--cat-4)", // green
  "var(--cat-3)", // yellow
  "var(--cat-7)", // blue
  "var(--cat-9)", // magenta
  "var(--cat-6)", // cyan
  "var(--fg-muted)", // white — body text, not a bright one
]

const BRIGHT: readonly string[] = [
  "var(--fg-muted)", // bright black is a mid grey
  "var(--cat-1)",
  "var(--cat-4)",
  "var(--cat-3)",
  "var(--cat-7)",
  "var(--cat-9)",
  "var(--cat-6)",
  "var(--fg)", // bright white — the strongest foreground the theme has
]

/** Hue anchors (sRGB degrees) for the ten ramp slots, in ramp order. */
const RAMP_HUES: readonly number[] = [0, 30, 50, 120, 165, 190, 220, 270, 300, 330]
const RAMP_VARS: readonly string[] = [
  "var(--cat-1)",
  "var(--cat-2)",
  "var(--cat-3)",
  "var(--cat-4)",
  "var(--cat-5)",
  "var(--cat-6)",
  "var(--cat-7)",
  "var(--cat-8)",
  "var(--cat-9)",
  "var(--cat-10)",
]

/** xterm's six cube levels. */
const CUBE_LEVELS = [0, 95, 135, 175, 215, 255]

/**
 * Maps an arbitrary RGB triple to the nearest ramp slot by hue, so a 256-colour
 * or 24-bit sequence keeps its intent (an angry red stays red) without
 * inheriting a lightness the current theme cannot show.
 */
function nearestRampColor(r: number, g: number, b: number): string {
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const chroma = max - min
  // Near-grey: no hue worth preserving, so pick a foreground weight instead.
  if (chroma < 24) {
    if (max < 96) return "var(--fg-subtle)"
    if (max < 192) return "var(--fg-muted)"
    return "var(--fg)"
  }
  let hue: number
  if (max === r) hue = ((g - b) / chroma) * 60
  else if (max === g) hue = ((b - r) / chroma) * 60 + 120
  else hue = ((r - g) / chroma) * 60 + 240
  hue = ((hue % 360) + 360) % 360

  let best = 0
  let bestDistance = Infinity
  for (let i = 0; i < RAMP_HUES.length; i++) {
    const raw = Math.abs(hue - RAMP_HUES[i])
    const distance = Math.min(raw, 360 - raw)
    if (distance < bestDistance) {
      bestDistance = distance
      best = i
    }
  }
  return RAMP_VARS[best]
}

/** Resolves a 256-colour palette index to a CSS colour. */
function color256(index: number): string {
  if (index < 8) return BASIC[index]
  if (index < 16) return BRIGHT[index - 8]
  if (index < 232) {
    const n = index - 16
    return nearestRampColor(
      CUBE_LEVELS[Math.floor(n / 36) % 6],
      CUBE_LEVELS[Math.floor(n / 6) % 6],
      CUBE_LEVELS[n % 6],
    )
  }
  if (index < 256) {
    const level = 8 + (index - 232) * 10
    return nearestRampColor(level, level, level)
  }
  return BASIC[7]
}

/**
 * A background out of a log line is mixed down towards transparent. The theme
 * owns the surface; honouring `ESC[47m` literally is how you get white on
 * white.
 */
function asBackground(color: string): string {
  return `color-mix(in srgb, ${color} 22%, transparent)`
}

/** Mutable SGR state while scanning. */
interface State {
  fg?: string
  bg?: string
  bold: boolean
  italic: boolean
  underline: boolean
  strike: boolean
  dim: boolean
  inverse: boolean
}

function reset(state: State): void {
  state.fg = undefined
  state.bg = undefined
  state.bold = false
  state.italic = false
  state.underline = false
  state.strike = false
  state.dim = false
  state.inverse = false
}

function freshState(): State {
  const state = {} as State
  reset(state)
  return state
}

/**
 * Reads one extended-colour argument (`38`/`48` followed by `5;n` or
 * `2;r;g;b`), returning the colour and how many parameters it consumed.
 */
function extendedColor(params: number[], at: number): { color?: string; consumed: number } {
  const mode = params[at + 1]
  if (mode === 5 && params.length > at + 2) {
    return { color: color256(params[at + 2]), consumed: 3 }
  }
  if (mode === 2 && params.length > at + 4) {
    return {
      color: nearestRampColor(params[at + 2], params[at + 3], params[at + 4]),
      consumed: 5,
    }
  }
  // Truncated or unknown extension: swallow the mode parameter and carry on.
  return { consumed: 2 }
}

function applySGR(state: State, raw: string): void {
  // An empty parameter list means SGR 0. Sub-parameters (`38:5:2`) are read as
  // parameters; the distinction only matters to terminals implementing the full
  // ITU form, which no logger emits.
  const params = raw === "" ? [0] : raw.split(/[;:]/).map((p) => (p === "" ? 0 : Number(p)))

  for (let i = 0; i < params.length; i++) {
    const code = params[i]
    if (Number.isNaN(code)) continue

    if (code === 0) reset(state)
    else if (code === 1) state.bold = true
    else if (code === 2) state.dim = true
    else if (code === 3) state.italic = true
    else if (code === 4) state.underline = true
    else if (code === 7) state.inverse = true
    else if (code === 9) state.strike = true
    else if (code === 21 || code === 22) {
      state.bold = false
      state.dim = false
    } else if (code === 23) state.italic = false
    else if (code === 24) state.underline = false
    else if (code === 27) state.inverse = false
    else if (code === 29) state.strike = false
    else if (code >= 30 && code <= 37) state.fg = BASIC[code - 30]
    else if (code === 38) {
      const { color, consumed } = extendedColor(params, i)
      if (color) state.fg = color
      i += consumed - 1
    } else if (code === 39) state.fg = undefined
    else if (code >= 40 && code <= 47) state.bg = BASIC[code - 40]
    else if (code === 48) {
      const { color, consumed } = extendedColor(params, i)
      if (color) state.bg = color
      i += consumed - 1
    } else if (code === 49) state.bg = undefined
    else if (code >= 90 && code <= 97) state.fg = BRIGHT[code - 90]
    else if (code >= 100 && code <= 107) state.bg = BRIGHT[code - 100]
  }
}

/** Snapshots the scan state as the style for the run that follows it. */
function styleOf(state: State): AnsiStyle {
  let fg = state.fg
  let bg = state.bg
  if (state.inverse) {
    // Reverse video swaps the two, each defaulting to what it would otherwise
    // have inherited from the surface.
    const swapped = bg ?? "var(--fg)"
    bg = fg ?? "var(--fg-muted)"
    fg = swapped
  }
  if (
    fg === undefined &&
    bg === undefined &&
    !state.bold &&
    !state.italic &&
    !state.underline &&
    !state.strike &&
    !state.dim
  ) {
    return PLAIN
  }
  const style: AnsiStyle = {}
  if (fg !== undefined) style.fg = fg
  if (bg !== undefined) style.bg = asBackground(bg)
  if (state.bold) style.bold = true
  if (state.italic) style.italic = true
  if (state.underline) style.underline = true
  if (state.strike) style.strike = true
  if (state.dim) style.dim = true
  return style
}

/**
 * Splits a log message into styled runs, dropping every escape sequence and
 * control byte it does not render. Always returns at least one segment, so a
 * caller can render the result without a special case for an empty message.
 */
export function parseAnsi(input: string): AnsiSegment[] {
  if (!input || !HAS_SEQUENCE.test(input)) return [{ text: input, style: PLAIN }]

  const segments: AnsiSegment[] = []
  const state = freshState()
  let style: AnsiStyle = PLAIN
  let pending = ""
  let index = 0

  const flush = () => {
    if (pending === "") return
    const last = segments.at(-1)
    // Consecutive runs that resolve to the same style are one run: a reset
    // followed by a colour is two sequences but one visual change.
    if (last !== undefined && sameStyle(last.style, style)) last.text += pending
    else segments.push({ text: pending, style })
    pending = ""
  }

  SEQUENCE.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = SEQUENCE.exec(input)) !== null) {
    pending += input.slice(index, match.index)
    index = match.index + match[0].length

    // Only SGR changes how the text that follows looks. Every other match is
    // simply dropped, having already been excluded from `pending`.
    if (match[2] !== "m") continue
    applySGR(state, match[1])
    const next = styleOf(state)
    if (!sameStyle(next, style)) {
      flush()
      style = next
    }
  }
  pending += input.slice(index)
  flush()

  return segments.length > 0 ? segments : [{ text: "", style: PLAIN }]
}

function sameStyle(a: AnsiStyle, b: AnsiStyle): boolean {
  if (a === b) return true
  return (
    a.fg === b.fg &&
    a.bg === b.bg &&
    a.bold === b.bold &&
    a.italic === b.italic &&
    a.underline === b.underline &&
    a.strike === b.strike &&
    a.dim === b.dim
  )
}

/**
 * The message as it reads without any styling — what a filter should match, a
 * level detector should scan, and a copy button should put on the clipboard.
 */
export function stripAnsi(input: string): string {
  if (!input || !HAS_SEQUENCE.test(input)) return input
  return input.replace(SEQUENCE, "")
}
