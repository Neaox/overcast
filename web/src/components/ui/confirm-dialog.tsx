import * as React from "react"
import { CircleAlert, TriangleAlert } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogKeyHint,
  DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/primitives"

interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: React.ReactNode
  confirmLabel?: string
  cancelLabel?: string
  variant?: "danger" | "default"
  isPending?: boolean
  /** Overrides the header tile glyph. Defaults to a warning/info triangle by variant. */
  icon?: React.ReactNode
  onConfirm: () => void
}

/**
 * Reusable confirmation dialog — typically used for destructive actions.
 *
 * Carries the full keyboard contract advertised in its footer: `⏎` runs the
 * confirm action (suppressed while the mutation is in flight) and `esc`
 * cancels via Radix's dismissable layer, which is the same path as Cancel.
 *
 * Usage:
 *   <ConfirmDialog
 *     open={!!deleteTarget}
 *     onOpenChange={(v) => !v && setDeleteTarget(undefined)}
 *     title="Delete Queue"
 *     description={<>Permanently delete <strong>{name}</strong>?</>}
 *     variant="danger"
 *     isPending={deleteMut.isPending}
 *     onConfirm={() => deleteMut.mutate(name)}
 *   />
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "Delete",
  cancelLabel = "Cancel",
  variant = "danger",
  isPending = false,
  icon,
  onConfirm,
}: ConfirmDialogProps) {
  const DefaultGlyph = variant === "danger" ? TriangleAlert : CircleAlert
  const contentRef = React.useRef<HTMLDivElement>(null)

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        ref={contentRef}
        size="sm"
        onPrimaryAction={onConfirm}
        primaryActionDisabled={isPending}
        onOpenAutoFocus={(event) => {
          // Focus the panel rather than the first tabbable control. That control
          // is Cancel, so the advertised "⏎ to delete" would otherwise dismiss
          // the dialog on the very first keypress — and auto-focusing the
          // destructive button instead is worse.
          event.preventDefault()
          contentRef.current?.focus()
        }}
      >
        <DialogHeader
          icon={icon ?? <DefaultGlyph />}
          tone={variant === "danger" ? "danger" : "accent"}
        >
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <p className="text-[13px] leading-relaxed text-fg-muted">{description}</p>
        <DialogFooter hint={<DialogKeyHint action={confirmLabel.toLowerCase()} />}>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {cancelLabel}
          </Button>
          <Button variant={variant} disabled={isPending} onClick={onConfirm}>
            {isPending && <Spinner className="mr-2" />}
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
