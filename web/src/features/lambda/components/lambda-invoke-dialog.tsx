import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { TestTab } from "@/features/lambda/components/test-tab"

interface Props {
  name: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function LambdaInvokeDialog({ name, open, onOpenChange }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>Test: {name}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <TestTab name={name} />
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}
