import Prism from "@/lib/prism"
import { formatTriggerEvent } from "./trigger-event-format"

export function TriggerEventViewer({ event }: { event: unknown }) {
  const formatted = formatTriggerEvent(event)
  const className = "wrap-break-word whitespace-pre-wrap"

  if (formatted.language === "json") {
    return (
      <pre
        className={className}
        dangerouslySetInnerHTML={{
          __html: Prism.highlight(formatted.text, Prism.languages.json, "json"),
        }}
      />
    )
  }

  return <pre className={className}>{formatted.text}</pre>
}
