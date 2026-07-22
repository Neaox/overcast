import { Link } from "@tanstack/react-router"
import { Bug } from "lucide-react"
import { Button } from "@/components/ui/button"

export function RawStateLink({
  service,
  namespace,
  stateKey,
  label = "Raw state",
}: {
  service?: string
  namespace?: string
  stateKey?: string
  label?: string
}) {
  return (
    <Button size="sm" variant="ghost" asChild>
      <Link to="/debug" search={{ service, namespace, key: stateKey }}>
        <Bug className="mr-1.5 h-3.5 w-3.5" />
        {label}
      </Link>
    </Button>
  )
}
