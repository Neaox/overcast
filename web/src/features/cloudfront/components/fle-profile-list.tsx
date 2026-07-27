import { useQuery } from "@tanstack/react-query"
import { ShieldCheck } from "lucide-react"
import { cloudfrontFLEProfilesQueryOptions } from "@/features/cloudfront/data"
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

export function FLEProfileList() {
  const {
    data: profiles = [],
    isLoading,
    isFetching,
    refetch,
    error,
  } = useQuery(cloudfrontFLEProfilesQueryOptions())

  return (
    <ResourceListPage
      title="Field-Level Encryption Profiles"
      count={profiles.length}
      actions={<RefreshAction isFetching={isFetching} onClick={() => refetch()} />}
    >
      <ResourceListCard>
        {isLoading || profiles.length === 0 ? (
          <QueryListState
            isLoading={isLoading}
            isEmpty={profiles.length === 0}
            error={error}
            empty={
              <EmptyState
                icon={<ShieldCheck className="h-10 w-10" />}
                title="No FLE profiles"
                description="Field-level encryption profiles define which public keys are used to encrypt specific fields."
              />
            }
            errorTitle="Failed to load FLE profiles"
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
              {profiles.map((p) => (
                <TableRow key={p.id}>
                  <TableCell>
                    <ResourceName icon={ShieldCheck} name={p.name} />
                  </TableCell>
                  <TableCell className="text-fg-muted">{p.id}</TableCell>
                  <TableCellProse>{p.comment || "—"}</TableCellProse>
                  <TableCell className="text-fg-muted">
                    {p.lastModifiedTime ? new Date(p.lastModifiedTime).toLocaleString() : "—"}
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
