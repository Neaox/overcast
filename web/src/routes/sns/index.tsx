import { createFileRoute } from "@tanstack/react-router"
import { PlaceholderPage } from "@/components/layout/placeholder-page"
import { Bell } from "lucide-react"

export const Route = createFileRoute("/sns/")({
  component: () => (
    <PlaceholderPage
      icon={<Bell className="h-10 w-10" />}
      service="SNS"
      description="Manage topics, subscriptions, and publish messages."
    />
  ),
})
