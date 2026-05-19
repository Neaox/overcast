import { useQuery } from "@tanstack/react-query"
import { Lock, RefreshCw } from "lucide-react"
import { cloudfrontFLEConfigsQueryOptions } from "@/features/cloudfront/data"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { PageHeader, Spinner, EmptyState } from "@/components/ui/primitives"
import { cn } from "@/lib/utils"

export function FLEConfigList() {
  const {
    data: configs = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontFLEConfigsQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Field-Level Encryption Configs"
        description={`${configs.length} config${configs.length !== 1 ? "s" : ""}`}
        actions={
          <Button size="sm" variant="ghost" onClick={() => refetch()} disabled={isFetching}>
            <RefreshCw className={cn("mr-1.5 h-3.5 w-3.5", isFetching && "animate-spin")} />
            Refresh
          </Button>
        }
      />

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : configs.length === 0 ? (
        <EmptyState
          icon={<Lock className="h-10 w-10" />}
          title="No FLE configs"
          description="Field-level encryption configurations specify which fields in POST requests should be encrypted."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Comment</TableHead>
              <TableHead>Unknown Content Type</TableHead>
              <TableHead>Last Modified</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {configs.map((c) => (
              <TableRow key={c.id}>
                <TableCell className="font-mono text-xs">{c.id}</TableCell>
                <TableCell className="text-fg-muted">{c.comment || "—"}</TableCell>
                <TableCell>{c.contentTypeProfileConfig}</TableCell>
                <TableCell className="text-fg-muted">
                  {c.lastModifiedTime ? new Date(c.lastModifiedTime).toLocaleString() : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
