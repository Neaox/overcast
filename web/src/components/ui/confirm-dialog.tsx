import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
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
  onConfirm: () => void
}

/**
 * Reusable confirmation dialog — typically used for destructive actions.
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
  onConfirm,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-fg-muted">{description}</p>
        <DialogFooter>
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
