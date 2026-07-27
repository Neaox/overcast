import { useQuery } from "@tanstack/react-query"
import { Key } from "lucide-react"
import { cloudfrontKeyGroupsQueryOptions } from "@/features/cloudfront/data"
import {
  Table,
  TableBody,
  TableCell,
  TableCellProse,
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

export function KeyGroupList() {
  const {
    data: keyGroups = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontKeyGroupsQueryOptions())

  return (
    <ResourceListPage
      title="Key Groups"
      count={keyGroups.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceListCard>
        {isLoading || keyGroups.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={keyGroups.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<Key className="h-10 w-10" />}
                title="No key groups"
                description="Key groups contain public keys used to verify signed URLs and signed cookies."
              />
            }
            errorTitle="Failed to load key groups"
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>ID</TableHead>
                <TableHead>Comment</TableHead>
                <TableHead>Last modified</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keyGroups.map((kg) => (
                <TableRow key={kg.id}>
                  <TableCell>
                    <ResourceName icon={Key} name={kg.name} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{kg.id}</TableCell>
                  <TableCellProse>{kg.comment || "—"}</TableCellProse>
                  <TableCell className="text-fg-muted">
                    {kg.lastModifiedTime ? new Date(kg.lastModifiedTime).toLocaleString() : "—"}
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
