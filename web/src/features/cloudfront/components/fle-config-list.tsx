import { useQuery } from "@tanstack/react-query"
import { Lock } from "lucide-react"
import { cloudfrontFLEConfigsQueryOptions } from "@/features/cloudfront/data"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EmptyState, QueryListState } from "@/components/ui/primitives"
import {
  RefreshAction,
  ResourceListCard,
  ResourceListPage,
  ResourceName,
} from "@/components/ui/resource-list-page"

export function FLEConfigList() {
  const {
    data: configs = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontFLEConfigsQueryOptions())

  return (
    <ResourceListPage
      title="Field-Level Encryption Configs"
      count={configs.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceListCard>
        {isLoading || configs.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={configs.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Lock className="h-10 w-10" />}
                title="No FLE configs"
                description="Field-level encryption configurations specify which fields in POST requests should be encrypted."
              />
            }
            errorTitle="Failed to load FLE configs"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead>Unknown content type</TableHead>
                <TableHead>Last modified</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {configs.map((c) => (
                <TableRow key={c.id}>
                  <TableCell>
                    <ResourceName icon={Lock} name={c.id} />
                  </TableCell>
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
      </ResourceListCard>
    </ResourceListPage>
  )
}
