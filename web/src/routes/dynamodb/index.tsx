import { createFileRoute } from "@tanstack/react-router"
import { TableList } from "@/features/dynamodb/components/table-list"

export const Route = createFileRoute("/dynamodb/")({
  head: () => ({ meta: [{ title: "DynamoDB Tables — Overcast" }] }),
  component: TableList,
})
