import { useQuery } from "@tanstack/react-query"
import { Key, RefreshCw } from "lucide-react"
import { cloudfrontKeyGroupsQueryOptions } from "@/features/cloudfront/data"
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

export function KeyGroupList() {
  const {
    data: keyGroups = [],
    isLoading,
    isFetching,
    refetch,
  } = useQuery(cloudfrontKeyGroupsQueryOptions())

  return (
    <div className="flex w-full flex-col gap-4">
      <PageHeader
        title="Key Groups"
        description={`${keyGroups.length} key group${keyGroups.length !== 1 ? "s" : ""}`}
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
      ) : keyGroups.length === 0 ? (
        <EmptyState
          icon={<Key className="h-10 w-10" />}
          title="No key groups"
          description="Key groups contain public keys used to verify signed URLs and signed cookies."
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
            {keyGroups.map((kg) => (
              <TableRow key={kg.id}>
                <TableCell className="font-mono text-xs">{kg.id}</TableCell>
                <TableCell className="font-medium">{kg.name}</TableCell>
                <TableCell className="text-fg-muted">{kg.comment || "—"}</TableCell>
                <TableCell className="text-fg-muted">
                  {kg.lastModifiedTime ? new Date(kg.lastModifiedTime).toLocaleString() : "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
