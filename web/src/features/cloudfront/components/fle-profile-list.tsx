import { useQuery } from "@tanstack/react-query"
import { ShieldCheck, RefreshCw } from "lucide-react"
import { cloudfrontFLEProfilesQueryOptions } from "@/features/cloudfront/data"
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

export function FLEProfileList() {
  const {
    data: profiles = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontFLEProfilesQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Field-Level Encryption Profiles"
        description={`${profiles.length} profile${profiles.length !== 1 ? "s" : ""}`}
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
      ) : profiles.length === 0 ? (
        <EmptyState
          icon={<ShieldCheck className="h-10 w-10" />}
          title="No FLE profiles"
          description="Field-level encryption profiles define which public keys are used to encrypt specific fields."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Comment</TableHead>
              <TableHead>Last Modified</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {profiles.map((p) => (
              <TableRow key={p.id}>
                <TableCell className="font-mono text-xs">{p.id}</TableCell>
                <TableCell className="font-medium">{p.name}</TableCell>
                <TableCell className="text-fg-muted">{p.comment || "—"}</TableCell>
                <TableCell className="text-fg-muted">
                  {p.lastModifiedTime ? new Date(p.lastModifiedTime).toLocaleString() : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
