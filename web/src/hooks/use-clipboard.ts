import { useCallback, useEffect, useRef, useState } from "react"
import { useToast } from "@/components/ui/toast"
import { writeClipboardText } from "@/lib/clipboard"

/**
 * How long a copy button holds its acknowledgement tick before reverting.
 * Long enough to register, short enough that a second copy reads as a second
 * copy rather than a stuck control.
 */
const ACKNOWLEDGEMENT_MS = 1500

export interface CopyOptions {
  /**
   * Names the thing copied, for the confirmation toast: `noun: "API key"`
   * reads as `Copied API key`. Omitted, the toast is a bare `Copied`.
   */
  noun?: string
  /**
   * Distinguishes which of several copy controls was used, so that only that
   * one shows the tick — compare against `copiedId`. A lone control can ignore
   * this and read `copied`.
   */
  id?: string
}

export interface CopyToClipboard {
  /** Copies `text` and reports the outcome as a toast. */
  copy: (text: string, options?: CopyOptions) => void
  /** A copy landed within the last {@link ACKNOWLEDGEMENT_MS}. */
  copied: boolean
  /** The `id` of the control whose copy landed, `""` when none was given. */
  copiedId: string | undefined
}

/**
 * Copy-to-clipboard with the feedback the design asks for: a toast either way,
 * and a transient flag for the control's own tick.
 *
 * Both outcomes are reported. A failure toast matters more than the success
 * one — a copy button that quietly does nothing is indistinguishable from a
 * copy button that worked, and outside a secure context that is precisely what
 * a bare `navigator.clipboard` call used to be (see `lib/clipboard.ts`).
 *
 * Prefer `<CopyButton>` (`components/ui/copy-button.tsx`), which wraps this.
 * Reach for the hook directly only when the trigger is not a button — a copy
 * item in a context menu, or a keyboard shortcut.
 */
export function useCopyToClipboard(): CopyToClipboard {
  const { toast } = useToast()
  const [copiedId, setCopiedId] = useState<string>()
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  useEffect(() => () => clearTimeout(timer.current), [])

  const copy = useCallback(
    (text: string, { noun, id = "" }: CopyOptions = {}) => {
      void writeClipboardText(text).then(
        () => {
          toast({ title: noun ? `Copied ${noun}` : "Copied", variant: "success" })
          setCopiedId(id)
          clearTimeout(timer.current)
          timer.current = setTimeout(() => setCopiedId(undefined), ACKNOWLEDGEMENT_MS)
        },
        (error: Error) =>
          toast({
            title: noun ? `Could not copy ${noun}` : "Could not copy",
            description: error.message,
            variant: "danger",
          }),
      )
    },
    [toast],
  )

  return { copy, copied: copiedId !== undefined, copiedId }
}
