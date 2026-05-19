import { useState } from "react"
import { Plus, Trash2 } from "lucide-react"
import { useMutation } from "@tanstack/react-query"
import { publishMutationOptions } from "@/features/sns/data"
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
import { Spinner } from "@/components/ui/primitives"
import { useToast } from "@/components/ui/toast"

const DATA_TYPES = ["String", "Number", "Binary"]

interface MessageAttributeRow {
  id: number
  name: string
  dataType: string
  value: string
}

let nextId = 0

interface Props {
  topicName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function PublishMessageDialog({ topicName, open, onOpenChange }: Props) {
  const { toast } = useToast()
  const [message, setMessage] = useState("")
  const [subject, setSubject] = useState("")
  const [messageStructureJson, setMessageStructureJson] = useState(false)
  const [messageGroupId, setMessageGroupId] = useState("")
  const [messageDeduplicationId, setMessageDeduplicationId] = useState("")
  const [attrRows, setAttrRows] = useState<MessageAttributeRow[]>([])

  const isFifoTopic = topicName.endsWith(".fifo")

  const mut = useMutation({
    ...publishMutationOptions(topicName),
    onSuccess: (data) => {
      toast({
        title: "Message published",
        description: `MessageId: ${data.MessageId}`,
        variant: "success",
      })
      resetForm()
      onOpenChange(false)
    },
    onError: (err: Error) => {
      toast({
        title: "Publish failed",
        description: err.message,
        variant: "danger",
      })
    },
  })

  function resetForm() {
    setMessage("")
    setSubject("")
    setMessageStructureJson(false)
    setMessageGroupId("")
    setMessageDeduplicationId("")
    setAttrRows([])
  }

  function handleClose() {
    resetForm()
    onOpenChange(false)
  }

  function addAttr() {
    setAttrRows((rows) => [...rows, { id: nextId++, name: "", dataType: "String", value: "" }])
  }

  function removeAttr(id: number) {
    setAttrRows((rows) => rows.filter((row) => row.id !== id))
  }

  function updateAttr(id: number, field: keyof MessageAttributeRow, value: string) {
    setAttrRows((rows) => rows.map((row) => (row.id === id ? { ...row, [field]: value } : row)))
  }

  function handlePublish() {
    if (!message.trim()) return

    const messageAttributes: Record<string, { dataType: string; stringValue: string }> = {}
    for (const row of attrRows) {
      const key = row.name.trim()
      if (!key) continue
      messageAttributes[key] = {
        dataType: row.dataType,
        stringValue: row.value,
      }
    }

    mut.mutate({
      message,
      subject: subject.trim() || undefined,
      messageStructure: messageStructureJson ? "json" : undefined,
      messageGroupId: isFifoTopic && messageGroupId.trim() ? messageGroupId.trim() : undefined,
      messageDeduplicationId:
        isFifoTopic && messageDeduplicationId.trim() ? messageDeduplicationId.trim() : undefined,
      messageAttributes: Object.keys(messageAttributes).length > 0 ? messageAttributes : undefined,
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (!nextOpen) {
          handleClose()
          return
        }
        onOpenChange(true)
      }}
    >
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Publish Message to {topicName}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <FormField label="Message" required>
            <textarea
              className="min-h-32 w-full rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg placeholder:text-fg-subtle focus:ring-2 focus:ring-accent/50 focus:outline-none"
              placeholder={messageStructureJson ? '{ "default": "hello" }' : "Message text"}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
            />
          </FormField>

          <div className="flex flex-wrap items-center gap-5">
            <label className="flex items-center gap-2 text-sm text-fg-muted">
              <input
                type="checkbox"
                checked={messageStructureJson}
                onChange={(e) => setMessageStructureJson(e.target.checked)}
              />
              Use JSON message structure
            </label>
            <span className="text-xs text-fg-subtle">
              Set this when sending protocol-specific payload JSON.
            </span>
          </div>

          <FormField label="Subject (optional)">
            <Input
              placeholder="Optional subject"
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              maxLength={100}
            />
          </FormField>

          {isFifoTopic && (
            <div className="grid grid-cols-1 gap-4 @md:grid-cols-2">
              <FormField label="Message Group ID" required>
                <Input
                  placeholder="e.g. notifications"
                  value={messageGroupId}
                  onChange={(e) => setMessageGroupId(e.target.value)}
                />
              </FormField>
              <FormField
                label="Deduplication ID"
                hint="Optional if content-based deduplication is enabled"
              >
                <Input
                  placeholder="Optional"
                  value={messageDeduplicationId}
                  onChange={(e) => setMessageDeduplicationId(e.target.value)}
                />
              </FormField>
            </div>
          )}

          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-fg-muted">Message Attributes</span>
              <Button size="sm" variant="ghost" type="button" onClick={addAttr}>
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
                      value={row.value}
                      onChange={(e) => updateAttr(row.id, "value", e.target.value)}
                    />
                    <Button
                      size="icon"
                      variant="ghost"
                      className="text-fg-muted hover:text-danger"
                      type="button"
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
          <Button variant="ghost" type="button" onClick={handleClose}>
            Cancel
          </Button>
          <Button
            type="button"
            onClick={handlePublish}
            disabled={!message.trim() || mut.isPending || (isFifoTopic && !messageGroupId.trim())}
          >
            {mut.isPending && <Spinner className="mr-2" />}
            Publish
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
