import * as React from "react"
import { Check, Copy } from "lucide-react"
import { cva } from "class-variance-authority"
import { Button, type ButtonProps } from "@/components/ui/button"
import { useCopyToClipboard } from "@/hooks/use-clipboard"
import { cn } from "@/lib/utils"

/**
 * Two treatments, because the app draws copy in two places: as a control in a
 * row of controls, and as a glyph tucked against the value it copies.
 *
 * `control` is the ghost icon button. `inline` drops the box entirely — no
 * padding, no hover fill, subtle until hovered — for the ARN, endpoint and
 * secret rows where a 24px button beside a 20px line would be the loudest
 * thing on the page.
 */
const copyButtonVariants = cva("shrink-0", {
  variants: {
    tone: {
      control: "",
      inline: "h-auto w-auto p-0 text-fg-subtle hover:bg-transparent hover:text-fg",
    },
  },
  defaultVariants: { tone: "control" },
})

export interface CopyButtonProps extends Omit<ButtonProps, "value" | "onClick" | "children"> {
  /** The text placed on the clipboard. */
  value: string
  /**
   * Names what is being copied, in the object position of both messages the
   * user sees: `noun="repository URI"` gives the button the label
   * `Copy repository URI` and the toast `Copied repository URI`. Omit it only
   * where the surrounding context already says what the value is.
   */
  noun?: string
  tone?: "control" | "inline"
}

/**
 * The one way to put a copy affordance on screen.
 *
 * It owns the parts every hand-rolled copy button got slightly differently: the
 * clipboard call (which must work outside a secure context — see
 * `lib/clipboard.ts`), the tick that acknowledges the copy, the success and
 * failure toasts, and an accessible name, which an icon-only button otherwise
 * does not have.
 *
 * @example
 * <CopyButton value={queue.url} noun="queue URL" />
 * <CopyButton value={arn} noun="ARN" tone="inline" />
 */
export const CopyButton = React.forwardRef<HTMLButtonElement, CopyButtonProps>(
  ({ value, noun, tone, className, variant = "ghost", size = "icon-sm", ...props }, ref) => {
    const { copy, copied } = useCopyToClipboard()
    const label = noun ? `Copy ${noun}` : "Copy"

    return (
      <Button
        ref={ref}
        type="button"
        variant={variant}
        size={size}
        aria-label={label}
        title={label}
        onClick={(event) => {
          // Copy controls sit inside clickable rows and draggable map nodes;
          // none of them should also select, navigate or expand.
          event.stopPropagation()
          copy(value, { noun })
        }}
        className={cn(copyButtonVariants({ tone }), className)}
        {...props}
      >
        {copied ? (
          <Check aria-hidden className="h-3.5 w-3.5 text-success" />
        ) : (
          <Copy aria-hidden className="h-3.5 w-3.5" />
        )}
      </Button>
    )
  },
)
CopyButton.displayName = "CopyButton"
