import { useState, useEffect } from "react"
import { cfnKeys, updateStackMutationOptions } from "@/features/cloudformation/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { FormField, FormRow } from "@/components/ui/form"
import { Spinner } from "@/components/ui/primitives"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

interface Props {
  stackName: string
  currentTemplate: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function UpdateStackDialog({ stackName, currentTemplate, open, onOpenChange }: Props) {
  const [templateBody, setTemplateBody] = useState(currentTemplate)

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (open) setTemplateBody(currentTemplate)
  }, [open, currentTemplate])

  const updateMut = useResourceMutation({
    options: updateStackMutationOptions(),
    invalidateKeys: [
      cfnKeys.stacks(),
      cfnKeys.stackDetail(stackName),
      cfnKeys.resourceList(stackName),
      cfnKeys.eventList(stackName),
      cfnKeys.templateDetail(stackName),
    ],
    successTitle: "Stack update started",
    successDescription: () => stackName,
    errorTitle: "Update failed",
    onSuccess: () => onOpenChange(false),
  })

  function handleUpdate() {
    if (!templateBody.trim()) return
    updateMut.mutate({ StackName: stackName, TemplateBody: templateBody })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Update stack — {stackName}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-1">
          <FormRow>
            <FormField
              label="Template body (JSON or YAML)"
              required
              hint="Edit the template to update the stack resources."
            >
              <Textarea
                value={templateBody}
                onChange={(e) => setTemplateBody(e.target.value)}
                className="min-h-72 font-mono text-xs"
                spellCheck={false}
                autoFocus
              />
            </FormField>
          </FormRow>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleUpdate} disabled={!templateBody.trim() || updateMut.isPending}>
            {updateMut.isPending && <Spinner className="mr-1.5" />}
            Update stack
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
