import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { createStreamMutationOptions, kinesisKeys } from "@/features/kinesis/data"
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

interface CreateStreamDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateStreamDialog({ open, onOpenChange }: CreateStreamDialogProps) {
  const createMut = useResourceMutation({
    options: createStreamMutationOptions(),
    invalidateKeys: [kinesisKeys.streams()],
    successTitle: "Stream created",
    successDescription: ({ name }) => name,
    onSuccess: () => onOpenChange(false),
  })

  const form = useForm({
    defaultValues: { name: "", shardCount: 1 },
    onSubmit: ({ value }) => createMut.mutate({ name: value.name, shardCount: value.shardCount }),
  })

  function handleClose() {
    onOpenChange(false)
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Kinesis Stream</DialogTitle>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            e.stopPropagation()
            void form.handleSubmit()
          }}
          className="flex flex-col gap-4"
        >
          <form.Field name="name" validators={{ onChange: z.string().min(1, "Name is required") }}>
            {(field) => (
              <FormRow>
                <FormField
                  label="Stream Name"
                  htmlFor="stream-name"
                  error={fieldError(field.state.meta.errors)}
                >
                  <Input
                    id="stream-name"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    placeholder="my-stream"
                    autoFocus
                  />
                </FormField>
              </FormRow>
            )}
          </form.Field>
          <form.Field
            name="shardCount"
            validators={{
              onChange: z.number().int().min(1, "At least 1 shard required").max(200),
            }}
          >
            {(field) => (
              <FormRow>
                <FormField
                  label="Shard Count"
                  htmlFor="shard-count"
                  error={fieldError(field.state.meta.errors)}
                >
                  <Input
                    id="shard-count"
                    type="number"
                    min={1}
                    max={200}
                    value={field.state.value}
                    onChange={(e) => field.handleChange(Number(e.target.value))}
                  />
                </FormField>
              </FormRow>
            )}
          </form.Field>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={createMut.isPending}>
              {createMut.isPending ? (
                <>
                  <Spinner className="mr-1.5" /> Creating…
                </>
              ) : (
                "Create"
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
