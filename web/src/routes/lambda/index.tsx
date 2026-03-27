import { createFileRoute } from "@tanstack/react-router"
import { PlaceholderPage } from "@/components/layout/placeholder-page"
import { Zap } from "lucide-react"

export const Route = createFileRoute("/lambda/")({
  component: () => (
    <PlaceholderPage
      icon={<Zap className="h-10 w-10" />}
      service="Lambda"
      description="Browse functions, invoke them, and inspect logs."
    />
  ),
})
