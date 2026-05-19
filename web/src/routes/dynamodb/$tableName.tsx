import { createFileRoute } from "@tanstack/react-router"
import { TableDetail } from "@/features/dynamodb/components/table-detail"

export const Route = createFileRoute("/dynamodb/$tableName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.tableName} — DynamoDB — Overcast` }],
  }),
  component: function TableDetailRoute() {
    const { tableName } = Route.useParams()
    return <TableDetail tableName={tableName} />
  },
})
