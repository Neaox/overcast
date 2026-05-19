import { useState, useMemo } from "react"
import { useForm } from "@tanstack/react-form"
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"
import { Key, Plus, RefreshCw, Search } from "lucide-react"
import { kmsKeysQueryOptions, kmsKeys, createKeyMutationOptions } from "@/features/kms/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField, FormRow } from "@/components/ui/form"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { ServiceDocsButton, useDocsFromHash } from "@/features/docs/service-docs-modal"
import { InertBanner } from "@/components/inert-banner"
import { useToast } from "@/components/ui/toast"
import { cn } from "@/lib/utils"
import { ArnText } from "@/components/ui/arn-link"

export function KmsPage() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { toast } = useToast()
  const [showCreate, setShowCreate] = useState(false)
  const [filter, setFilter] = useState("")
  const [docsOpen, openDocs, closeDocs] = useDocsFromHash()

  const { data: keys = [], isLoading, isFetching, refetch } = useQuery(kmsKeysQueryOptions())

  const createMut = useMutation({
    ...createKeyMutationOptions(),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: kmsKeys.keys() })
      setShowCreate(false)
      toast({ title: "Key created", variant: "success" })
    },
    onError: (err: Error) =>
      toast({ title: "Create failed", description: err.message, variant: "danger" }),
  })

  const filtered = useMemo(
    () =>
      filter
        ? keys.filter((k) => (k.KeyId ?? "").toLowerCase().includes(filter.toLowerCase()))
        : keys,
    [keys, filter],
  )

  const form = useForm({
    defaultValues: { description: "" },
    onSubmit: ({ value }) => createMut.mutate(value.description || undefined),
  })

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="KMS"
        description="Customer-managed encryption keys"
        actions={
          <div className="flex items-center gap-2">
            <ServiceDocsButton
              service="kms"
              label="KMS"
              open={docsOpen}
              onOpen={openDocs}
              onClose={closeDocs}
            />
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              title="Refresh"
            >
              <RefreshCw className={cn("h-4 w-4", isFetching && "animate-spin")} />
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)}>
              <Plus className="mr-1 h-4 w-4" />
              Create key
            </Button>
          </div>
        }
      />
      <InertBanner serviceName="KMS" />

      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="text-muted-foreground absolute top-1/2 left-2 h-3.5 w-3.5 -translate-y-1/2" />
          <Input
            placeholder="Filter keys…"
            className="pl-8"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </div>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-24">
          <Spinner className="h-6 w-6" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={<Key className="h-8 w-8 opacity-40" />}
          title="No keys"
          description={filter ? "No keys match the filter." : "Create a key to get started."}
          action={
            !filter && (
              <Button size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="mr-1.5 h-3.5 w-3.5" /> Create key
              </Button>
            )
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Key ID</TableHead>
              <TableHead>ARN</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filtered.map((key) => (
              <TableRow
                key={key.KeyId}
                className="cursor-pointer"
                onClick={() => navigate({ to: "/kms/$keyId", params: { keyId: key.KeyId ?? "" } })}
              >
                <TableCell className="font-mono text-sm font-medium">{key.KeyId}</TableCell>
                <TableCell className="text-fg-muted">
                  <ArnText arn={key.KeyArn ?? ""} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      {/* Create key dialog */}
      <Dialog
        open={showCreate}
        onOpenChange={(open) => {
          setShowCreate(open)
          if (!open) form.reset()
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create key</DialogTitle>
          </DialogHeader>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void form.handleSubmit()
            }}
          >
            <div className="flex flex-col gap-4 py-2">
              <form.Field name="description">
                {(field) => (
                  <FormRow>
                    <FormField label="Description (optional)" htmlFor="key-description">
                      <Input
                        id="key-description"
                        placeholder="My encryption key"
                        value={field.state.value}
                        onBlur={field.handleBlur}
                        onChange={(e) => field.handleChange(e.target.value)}
                      />
                    </FormField>
                  </FormRow>
                )}
              </form.Field>
            </div>
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => setShowCreate(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createMut.isPending}>
                {createMut.isPending ? <Spinner className="mr-2 h-3.5 w-3.5" /> : null}
                Create
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
