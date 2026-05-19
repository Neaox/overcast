import { useState } from "react"
import { Eye, EyeOff, Copy, Check } from "lucide-react"
import { Button } from "@/components/ui/button"
import { useToast } from "@/components/ui/toast"

/**
 * Renders an API key value as a masked string with toggle-reveal and
 * copy-to-clipboard actions. Intended for use in tables (compact size).
 */
export function ApiKeyValue({ value }: { value?: string }) {
  const { toast } = useToast()
  const [revealed, setRevealed] = useState(false)
  const [copied, setCopied] = useState(false)

  if (!value) {
    return <span className="text-fg-muted">—</span>
  }

  // Show last 4 chars when masked, mask the rest with a fixed-length dot
  // sequence so different key lengths line up visually.
  const masked = `••••••••••••${value.slice(-4)}`

  const handleCopy = () => {
    void navigator.clipboard.writeText(value).then(() => {
      setCopied(true)
      toast({ title: "API key value copied" })
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div className="flex items-center gap-1.5">
      <span className="font-mono text-xs text-fg-muted">{revealed ? value : masked}</span>
      <Button
        size="sm"
        variant="ghost"
        className="h-6 w-6 p-0"
        onClick={() => setRevealed((v) => !v)}
        title={revealed ? "Hide value" : "Reveal value"}
      >
        {revealed ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        className="h-6 w-6 p-0"
        onClick={handleCopy}
        title="Copy value"
      >
        {copied ? <Check className="h-3.5 w-3.5 text-success" /> : <Copy className="h-3.5 w-3.5" />}
      </Button>
    </div>
  )
}
