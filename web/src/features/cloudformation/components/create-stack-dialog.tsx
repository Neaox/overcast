import { useState } from "react"
import { cfnKeys, createStackMutationOptions } from "@/features/cloudformation/data"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
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

const EXAMPLE_TEMPLATE = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "My stack",
  "Resources": {
    "MyQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {}
    }
  }
}`

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateStackDialog({ open, onOpenChange }: Props) {
  const [stackName, setStackName] = useState("")
  const [templateBody, setTemplateBody] = useState("")
  const [templateError, setTemplateError] = useState<string>()

  function reset() {
    setStackName("")
    setTemplateBody("")
    setTemplateError(undefined)
  }

  const createMut = useResourceMutation({
    options: createStackMutationOptions(),
    invalidateKeys: [cfnKeys.stacks()],
    successTitle: "Stack creation started",
    successDescription: () => stackName,
    errorTitle: "Create failed",
    onSuccess: () => {
      onOpenChange(false)
      reset()
    },
  })

  function validateTemplate(body: string): boolean {
    if (!body.trim()) {
      setTemplateError("Template body is required")
      return false
    }
    try {
      JSON.parse(body)
      setTemplateError(undefined)
      return true
    } catch {
      // might be YAML — accept it and let the backend validate
      setTemplateError(undefined)
      return true
    }
  }

  function handleCreate() {
    if (!stackName.trim()) return
    if (!validateTemplate(templateBody)) return
    createMut.mutate({ StackName: stackName.trim(), TemplateBody: templateBody })
  }

  function handleOpenChange(v: boolean) {
    if (!v) reset()
    onOpenChange(v)
  }

  const canSubmit = stackName.trim().length > 0 && templateBody.trim().length > 0 && !templateError

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Create stack</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-1">
          <FormRow>
            <FormField label="Stack name" required>
              <Input
                value={stackName}
                onChange={(e) => setStackName(e.target.value)}
                placeholder="my-stack"
                autoFocus
                onKeyDown={(e) => e.key === "Enter" && handleCreate()}
              />
            </FormField>
          </FormRow>

          <FormRow>
            <FormField
              label="Template body (JSON or YAML)"
              required
              hint={
                templateError === undefined
                  ? "Paste a CloudFormation template in JSON or YAML format."
                  : undefined
              }
              error={templateError}
            >
              <Textarea
                value={templateBody}
                onChange={(e) => {
                  setTemplateBody(e.target.value)
                  setTemplateError(undefined)
                }}
                placeholder={EXAMPLE_TEMPLATE}
                className="min-h-52 font-mono text-xs"
                spellCheck={false}
              />
            </FormField>
          </FormRow>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => handleOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={handleCreate} disabled={!canSubmit || createMut.isPending}>
            {createMut.isPending && <Spinner className="mr-1.5" />}
            Create stack
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
