import { useState } from "react"
import { Eye, EyeOff } from "lucide-react"
import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"

/**
 * Renders an API key value as a masked string with toggle-reveal and
 * copy-to-clipboard actions. Intended for use in tables (compact size).
 */
export function ApiKeyValue({ value }: { value?: string }) {
  const [revealed, setRevealed] = useState(false)

  if (!value) {
    return <span className="text-fg-muted">—</span>
  }

  // Show last 4 chars when masked, mask the rest with a fixed-length dot
  // sequence so different key lengths line up visually.
  const masked = `••••••••••••${value.slice(-4)}`

  return (
    <div className="flex items-center gap-1.5">
      <span className="font-mono text-xs text-fg-muted">{revealed ? value : masked}</span>
      <Button
        size="icon-sm"
        variant="ghost"
        onClick={() => setRevealed((v) => !v)}
        title={revealed ? "Hide value" : "Reveal value"}
      >
        {revealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
      </Button>
      <CopyButton value={value} noun="API key value" />
    </div>
  )
}
