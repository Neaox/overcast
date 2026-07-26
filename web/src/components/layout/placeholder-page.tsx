import { Construction } from "lucide-react"
import { Badge } from "@/components/ui/badge"

interface PlaceholderPageProps {
  icon?: React.ReactNode
  service: string
  description?: string
}

export function PlaceholderPage({ icon, service, description }: PlaceholderPageProps) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 p-8 text-center">
      <div className="mb-2 text-fg-subtle">{icon ?? <Construction className="h-10 w-10" />}</div>
      <h2 className="font-mono text-lg font-semibold text-fg">{service}</h2>
      {/* Prose, so it stays sans. */}
      {description && <p className="max-w-sm text-sm text-fg-muted">{description}</p>}
      <Badge variant="outline" className="px-3 py-1 text-fg-subtle">
        Coming soon
      </Badge>
    </div>
  )
}
