import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { createTopicMutationOptions, snsKeys } from "@/features/sns/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow, fieldError } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"

interface CreateTopicDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateTopicDialog({ open, onOpenChange }: CreateTopicDialogProps) {
  const createMut = useResourceMutation({
    options: createTopicMutationOptions(),
    invalidateKeys: [snsKeys.topics()],
    successTitle: "Topic created",
    successDescription: (name) => name,
    onSuccess: () => onOpenChange(false),
  })

  const form = useForm({
    validators: {
      onChange: z.object({
        name: z
          .string()
          .min(1, "Required")
          .max(256, "Max 256 chars")
          .regex(/^[A-Za-z0-9_-]+$/, "Letters, digits, - and _ only"),
      }),
    },
    defaultValues: { name: "" },
    onSubmit: ({ value }) => createMut.mutate(value.name),
  })

  function handleClose() {
    onOpenChange(false)
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create topic</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void form.handleSubmit()
          }}
        >
          <form.Field name="name">
            {(field) => (
              <FormRow>
                <FormField label="Topic name" error={fieldError(field.state.meta.errors)}>
                  <Input
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    placeholder="my-topic"
                    autoFocus
                  />
                </FormField>
              </FormRow>
            )}
          </form.Field>
          <DialogFooter className="mt-4">
            <Button variant="ghost" type="button" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={createMut.isPending}>
              {createMut.isPending ? <Spinner className="h-4 w-4" /> : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
