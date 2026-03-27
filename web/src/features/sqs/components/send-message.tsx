import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Plus, Trash2 } from "lucide-react"
import { sendMessageMutationOptions, sqsKeys } from "@/features/sqs/data"
import { useEndpoint } from "@/hooks/use-endpoint"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useToast } from "@/components/ui/toast"
import { Spinner } from "@/components/ui/primitives"

const DATA_TYPES = ["String", "Number", "Binary"]

interface MessageAttributeRow {
  id: number
  name: string
  dataType: string
  stringValue: string
}

let nextId = 0

interface Props {
  queueName: string
  open: boolean
  onClose: () => void
}

export function SendMessageDialog({ queueName, open, onClose }: Props) {
  const { endpoint } = useEndpoint()
  const qc = useQueryClient()
  const { toast } = useToast()

  const [body, setBody] = useState("")
  const [delaySeconds, setDelaySeconds] = useState("")
  const [attrRows, setAttrRows] = useState<MessageAttributeRow[]>([])

  const mutation = useMutation({
    ...sendMessageMutationOptions(queueName),
    onSuccess: ({ messageId }) => {
      qc.invalidateQueries({ queryKey: sqsKeys.messageList(endpoint.baseUrl, queueName) })
      qc.invalidateQueries({ queryKey: sqsKeys.queue(endpoint.baseUrl, queueName) })
      toast({ title: "Message sent", description: `ID: ${messageId}`, variant: "success" })
      handleClose()
    },
    onError: (err: Error) =>
      toast({ title: "Send failed", description: err.message, variant: "danger" }),
  })

  function handleClose() {
    setBody("")
    setDelaySeconds("")
    setAttrRows([])
    onClose()
  }

  function addAttr() {
    setAttrRows((r) => [...r, { id: nextId++, name: "", dataType: "String", stringValue: "" }])
  }

  function removeAttr(id: number) {
    setAttrRows((r) => r.filter((a) => a.id !== id))
  }

  function updateAttr(id: number, field: keyof MessageAttributeRow, value: string) {
    setAttrRows((r) => r.map((a) => (a.id === id ? { ...a, [field]: value } : a)))
  }

  function handleSend() {
    if (!body.trim()) return

    const messageAttributes: Record<string, { dataType: string; stringValue: string }> = {}
    for (const row of attrRows) {
      if (row.name.trim()) {
        messageAttributes[row.name.trim()] = {
          dataType: row.dataType,
          stringValue: row.stringValue,
        }
      }
    }

    mutation.mutate({
      body: body.trim(),
      delaySeconds: delaySeconds ? parseInt(delaySeconds, 10) : undefined,
      messageAttributes: Object.keys(messageAttributes).length > 0 ? messageAttributes : undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={(v) => !v && handleClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Send Message to {queueName}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <FormField label="Message Body" required>
            <textarea
              className="min-h-[120px] w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:ring-2 focus:ring-accent/50 focus:outline-none"
              placeholder="Enter message body..."
              value={body}
              onChange={(e) => setBody(e.target.value)}
            />
          </FormField>

          <FormField
            label="Delay (seconds)"
            hint="Override the queue delay for this message (0–900)"
          >
            <Input
              type="number"
              min={0}
              max={900}
              placeholder="Queue default"
              value={delaySeconds}
              onChange={(e) => setDelaySeconds(e.target.value)}
              className="w-40"
            />
          </FormField>

          {/* Message attributes */}
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-fg-muted">Message Attributes</span>
              <Button size="sm" variant="ghost" onClick={addAttr}>
                <Plus className="mr-1 h-3 w-3" />
                Add attribute
              </Button>
            </div>

            {attrRows.length > 0 && (
              <div className="flex flex-col gap-2">
                <div className="grid grid-cols-[1fr_120px_1fr_32px] gap-2 text-xs font-medium text-fg-muted">
                  <span>Name</span>
                  <span>Type</span>
                  <span>Value</span>
                  <span />
                </div>
                {attrRows.map((row) => (
                  <div
                    key={row.id}
                    className="grid grid-cols-[1fr_120px_1fr_32px] items-center gap-2"
                  >
                    <Input
                      placeholder="AttributeName"
                      value={row.name}
                      onChange={(e) => updateAttr(row.id, "name", e.target.value)}
                    />
                    <select
                      className="h-9 rounded-md border border-border bg-bg px-2 text-sm text-fg"
                      value={row.dataType}
                      onChange={(e) => updateAttr(row.id, "dataType", e.target.value)}
                    >
                      {DATA_TYPES.map((t) => (
                        <option key={t} value={t}>
                          {t}
                        </option>
                      ))}
                    </select>
                    <Input
                      placeholder="Value"
                      value={row.stringValue}
                      onChange={(e) => updateAttr(row.id, "stringValue", e.target.value)}
                    />
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      onClick={() => removeAttr(row.id)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={handleClose}>
            Cancel
          </Button>
          <Button onClick={handleSend} disabled={!body.trim() || mutation.isPending}>
            {mutation.isPending && <Spinner className="mr-2" />}
            Send Message
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
