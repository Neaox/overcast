import { useState, useCallback } from "react"
import { useResourceMutation } from "@/hooks/use-resource-mutation"
import { createTableMutationOptions, dynamoKeys } from "@/features/dynamodb/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow } from "@/components/ui/form"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/primitives"

interface CreateTableDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateTableDialog({ open, onOpenChange }: CreateTableDialogProps) {
  const [tableName, setTableName] = useState("")
  const [hashKeyName, setHashKeyName] = useState("")
  const [hashKeyType, setHashKeyType] = useState<"S" | "N" | "B">("S")
  const [sortKeyName, setSortKeyName] = useState("")
  const [sortKeyType, setSortKeyType] = useState<"S" | "N" | "B">("S")
  const [billingMode, setBillingMode] = useState<"PAY_PER_REQUEST" | "PROVISIONED">(
    "PAY_PER_REQUEST",
  )
  const [readCapacityUnits, setReadCapacityUnits] = useState("5")
  const [writeCapacityUnits, setWriteCapacityUnits] = useState("5")

  const resetForm = useCallback(() => {
    setTableName("")
    setHashKeyName("")
    setHashKeyType("S")
    setSortKeyName("")
    setSortKeyType("S")
    setBillingMode("PAY_PER_REQUEST")
    setReadCapacityUnits("5")
    setWriteCapacityUnits("5")
  }, [])

  // DynamoDB requires both capacity units under PROVISIONED and rejects them
  // under PAY_PER_REQUEST, so the form only collects them for the former.
  const provisioned = billingMode === "PROVISIONED"
  const capacityValid =
    !provisioned || (Number(readCapacityUnits) >= 1 && Number(writeCapacityUnits) >= 1)

  const createMut = useResourceMutation({
    options: createTableMutationOptions(),
    invalidateKeys: [dynamoKeys.tables()],
    successTitle: "Table created",
    successDescription: ({ tableName: name }) => name,
    errorTitle: "Create failed",
    onSuccess: () => {
      onOpenChange(false)
      resetForm()
    },
  })

  function handleCreate() {
    if (!tableName.trim() || !hashKeyName.trim() || !capacityValid) return
    createMut.mutate({
      tableName: tableName.trim(),
      hashKeyName: hashKeyName.trim(),
      hashKeyType,
      sortKeyName: sortKeyName.trim() || undefined,
      sortKeyType: sortKeyName.trim() ? sortKeyType : undefined,
      billingMode,
      readCapacityUnits: provisioned ? Number(readCapacityUnits) : undefined,
      writeCapacityUnits: provisioned ? Number(writeCapacityUnits) : undefined,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create Table</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3 py-2">
          <FormRow>
            <FormField label="Table name" required>
              <Input
                value={tableName}
                onChange={(e) => setTableName(e.target.value)}
                placeholder="my-table"
                autoFocus
              />
            </FormField>
          </FormRow>
          <FormRow>
            <FormField label="Partition key (HASH)" required>
              <div className="flex gap-2">
                <Input
                  value={hashKeyName}
                  onChange={(e) => setHashKeyName(e.target.value)}
                  placeholder="id"
                  className="flex-1"
                />
                <select
                  value={hashKeyType}
                  onChange={(e) => setHashKeyType(e.target.value as "S" | "N" | "B")}
                  className="rounded-md border border-border bg-bg px-2 text-sm text-fg"
                >
                  <option value="S">String (S)</option>
                  <option value="N">Number (N)</option>
                  <option value="B">Binary (B)</option>
                </select>
              </div>
            </FormField>
          </FormRow>
          <FormRow>
            <FormField label="Sort key (RANGE) — optional">
              <div className="flex gap-2">
                <Input
                  value={sortKeyName}
                  onChange={(e) => setSortKeyName(e.target.value)}
                  placeholder="timestamp (optional)"
                  className="flex-1"
                />
                <select
                  value={sortKeyType}
                  onChange={(e) => setSortKeyType(e.target.value as "S" | "N" | "B")}
                  className="rounded-md border border-border bg-bg px-2 text-sm text-fg"
                  disabled={!sortKeyName}
                >
                  <option value="S">String (S)</option>
                  <option value="N">Number (N)</option>
                  <option value="B">Binary (B)</option>
                </select>
              </div>
            </FormField>
          </FormRow>
          <FormRow>
            <FormField label="Billing mode" required>
              <select
                value={billingMode}
                onChange={(e) =>
                  setBillingMode(e.target.value as "PAY_PER_REQUEST" | "PROVISIONED")
                }
                className="w-full rounded-md border border-border bg-bg px-2 py-1.5 text-sm text-fg"
              >
                <option value="PAY_PER_REQUEST">On-demand (PAY_PER_REQUEST)</option>
                <option value="PROVISIONED">Provisioned (PROVISIONED)</option>
              </select>
            </FormField>
          </FormRow>
          {provisioned && (
            <FormRow>
              <FormField label="Read capacity units" required>
                <Input
                  type="number"
                  min={1}
                  value={readCapacityUnits}
                  onChange={(e) => setReadCapacityUnits(e.target.value)}
                />
              </FormField>
              <FormField label="Write capacity units" required>
                <Input
                  type="number"
                  min={1}
                  value={writeCapacityUnits}
                  onChange={(e) => setWriteCapacityUnits(e.target.value)}
                />
              </FormField>
            </FormRow>
          )}
        </div>
        <DialogFooter>
          <Button
            variant="ghost"
            onClick={() => {
              onOpenChange(false)
              resetForm()
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={handleCreate}
            disabled={
              !tableName.trim() || !hashKeyName.trim() || !capacityValid || createMut.isPending
            }
          >
            {createMut.isPending ? <Spinner className="mr-1.5" /> : null}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
