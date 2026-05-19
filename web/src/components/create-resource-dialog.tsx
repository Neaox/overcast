import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import type { UseMutationOptions, WithRequired } from "@tanstack/react-query"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
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

interface CreateResourceDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  label: string
  placeholder: string
  mutationOptions: () => WithRequired<UseMutationOptions<void, Error, string>, "mutationKey">
  invalidateKeys: (readonly unknown[])[]
  successTitle: string
}

export function CreateResourceDialog({
  open,
  onOpenChange,
  title,
  label,
  placeholder,
  mutationOptions: getMutationOptions,
  invalidateKeys,
  successTitle,
}: CreateResourceDialogProps) {
  const createMut = useResourceMutation({
    options: getMutationOptions(),
    invalidateKeys,
    successTitle,
    successDescription: (name: string) => name,
    onSuccess: () => onOpenChange(false),
  })

  const form = useForm({
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
          <DialogTitle>{title}</DialogTitle>
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
                  label={label}
                  htmlFor="resource-name"
                  error={fieldError(field.state.meta.errors)}
                >
                  <Input
                    id="resource-name"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    placeholder={placeholder}
                    autoFocus
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
