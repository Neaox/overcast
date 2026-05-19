import { useForm } from "@tanstack/react-form"
import { z } from "zod"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { createRestApiMutationOptions, apigwKeys } from "@/features/apigateway/data"
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

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateRestApiDialog({ open, onOpenChange }: Props) {
  const createMut = useResourceMutation({
    options: createRestApiMutationOptions(),
    invalidateKeys: [apigwKeys.restApis()],
    successTitle: "REST API created",
    successDescription: ({ name }) => name,
    onSuccess: () => onOpenChange(false),
  })

  const form = useForm({
    defaultValues: { name: "", description: "" },
    onSubmit: ({ value }) =>
      createMut.mutate({
        name: value.name,
        description: value.description || undefined,
      }),
  })

  function handleClose() {
    onOpenChange(false)
    setTimeout(() => form.reset(), 150)
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create REST API</DialogTitle>
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
                  label="API Name"
                  htmlFor="rest-api-name"
                  error={fieldError(field.state.meta.errors)}
                >
                  <Input
                    id="rest-api-name"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    placeholder="my-rest-api"
                    autoFocus
                  />
                </FormField>
              </FormRow>
            )}
          </form.Field>
          <form.Field name="description">
            {(field) => (
              <FormRow>
                <FormField label="Description" htmlFor="rest-api-desc">
                  <Input
                    id="rest-api-desc"
                    value={field.state.value}
                    onChange={(e) => field.handleChange(e.target.value)}
                    placeholder="Optional description"
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
              {createMut.isPending && <Spinner className="mr-2 h-3.5 w-3.5" />}
              Create
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
