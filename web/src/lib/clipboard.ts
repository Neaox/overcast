/**
 * Clipboard access that survives an insecure browsing context.
 *
 * `navigator.clipboard` is gated behind a secure context, so the whole async
 * Clipboard API is simply *absent* whenever Overcast is served over plain HTTP
 * from anything that is not `localhost` — `http://localhost.overcast.sh:5173`,
 * a LAN address, a container hostname on a shared network. Reading
 * `navigator.clipboard.writeText` there throws, which is why every copy button
 * in the app goes through `writeClipboardText` instead: it prefers the async
 * API wherever it exists and otherwise falls back to a hidden `<textarea>`
 * driven by `document.execCommand("copy")`. That command is deprecated, but it
 * is implemented by every browser we support and carries no secure-context
 * requirement, which makes it the only fallback there is.
 *
 * Nothing else in `src/` may touch `navigator.clipboard` — `clipboard.test.ts`
 * fails the build if it does, because a direct call is exactly the bug this
 * module exists to prevent.
 */

/**
 * The DOM lib types `navigator.clipboard` as always present and its methods as
 * always implemented. Neither holds: the object is missing outside a secure
 * context, and Firefox ships `writeText` without ever exposing `readText` to
 * page scripts. Reading through a widened view is what lets those absences be
 * handled rather than assumed away.
 */
type PartialClipboard = Partial<Pick<Clipboard, "writeText" | "readText">>

function asyncClipboard(): PartialClipboard | undefined {
  return (navigator as { clipboard?: PartialClipboard }).clipboard
}

/**
 * Copies `text`, resolving once it is on the clipboard.
 *
 * Rejects with a user-presentable message when every route is blocked, so a
 * caller can surface the failure instead of leaving a button that silently
 * does nothing.
 */
export async function writeClipboardText(text: string): Promise<void> {
  const clipboard = asyncClipboard()
  if (clipboard?.writeText) {
    try {
      await clipboard.writeText(text)
      return
    } catch {
      // `writeText` also rejects for a denied permission or an unfocused
      // document, and the synchronous fallback recovers from both — so this
      // falls through rather than failing here.
    }
  }
  if (!writeViaExecCommand(text)) {
    throw new Error("The browser blocked access to the clipboard")
  }
}

/**
 * Whether the clipboard can be *read*.
 *
 * Unlike writing, reading has no fallback at all: `document.execCommand("paste")`
 * is refused by every browser, and Firefox does not implement `readText` for
 * page scripts in any context. A paste affordance must therefore ask first and
 * tell the user to paste by hand when the answer is no.
 */
export function canReadClipboardText(): boolean {
  return asyncClipboard()?.readText !== undefined
}

/** Reads the clipboard. Only call when {@link canReadClipboardText} is true. */
export async function readClipboardText(): Promise<string> {
  const readText = asyncClipboard()?.readText
  if (!readText) throw new Error("This browser does not allow reading the clipboard")
  return readText.call(navigator.clipboard)
}

/**
 * Copies by selecting the text inside an off-screen `<textarea>` and asking the
 * document to copy its selection. Returns whether the copy took.
 *
 * Three details are load-bearing:
 *
 * - The textarea is parked beside whatever currently holds focus rather than on
 *   `document.body`. Every dialog, popover and menu in the app is a Radix focus
 *   scope, and a scope pulls focus straight back the moment it lands outside
 *   the trapped subtree — which would drop the selection before the copy runs.
 *   Mounting inside the focused element's parent keeps it within whatever scope
 *   is active, and degrades to `document.body` on an untrapped page.
 * - It is `readonly`, which stops iOS raising the software keyboard, and it is
 *   selected with `setSelectionRange` rather than `select()` alone, because
 *   that is the only selection call iOS Safari honours on a readonly field.
 * - It is transparent and 1px rather than `display: none` or `hidden`: a
 *   genuinely non-rendered field cannot hold a selection.
 *
 * The element is added and removed within a single task, so React never
 * observes a child it did not render, and the caller's selection and focus are
 * both put back.
 */
function writeViaExecCommand(text: string): boolean {
  const focused = document.activeElement
  const parent = focused?.parentElement
  const host = parent && parent !== document.documentElement ? parent : document.body

  const selection = document.getSelection()
  const previousRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0) : null

  const textarea = document.createElement("textarea")
  textarea.value = text
  textarea.setAttribute("readonly", "")
  textarea.setAttribute("aria-hidden", "true")
  textarea.setAttribute("tabindex", "-1")
  textarea.style.cssText =
    "position:fixed;top:0;left:0;width:1px;height:1px;padding:0;border:0;margin:0;opacity:0;pointer-events:none;"
  host.appendChild(textarea)

  try {
    textarea.focus({ preventScroll: true })
    textarea.select()
    textarea.setSelectionRange(0, text.length)
    return document.execCommand("copy")
  } catch {
    // Safari throws rather than returning false when the copy is not allowed.
    return false
  } finally {
    textarea.remove()
    if (selection && previousRange) {
      selection.removeAllRanges()
      selection.addRange(previousRange)
    }
    if (focused instanceof HTMLElement) focused.focus({ preventScroll: true })
  }
}
