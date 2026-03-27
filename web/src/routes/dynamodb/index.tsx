import { createFileRoute } from "@tanstack/react-router"
import { PlaceholderPage } from "@/components/layout/placeholder-page"
import { Database } from "lucide-react"

export const Route = createFileRoute("/dynamodb/")({
  component: () => (
    <PlaceholderPage
      icon={<Database className="h-10 w-10" />}
      service="DynamoDB"
      description="Manage tables, browse items, and run queries."
    />
  ),
})
