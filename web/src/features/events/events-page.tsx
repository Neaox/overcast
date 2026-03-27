/**
 * EventsPage — top-level event stream viewer at /events.
 *
 * Shows a live, virtual-scrolled console of all events published on the
 * emulator's internal event bus. Filter by service source using the
 * toggle buttons above the console.
 *
 * Generic by design — adding DynamoDB streams, SQS delivery events, etc.
 * only requires adding the service name to SOURCES below.
 */
import { useState } from "react"
import { Activity } from "lucide-react"
import { useEventStream } from "@/hooks/use-event-stream"
import { EventConsole } from "@/components/ui/event-console"
import { PageHeader } from "@/components/ui/primitives"
import { Button } from "@/components/ui/button"

// ─── Source registry ─────────────────────────────────────────────────────────
// Add a new entry here when a service gains event support.

const SOURCES = [
  { id: "s3", label: "S3", color: "text-orange-400" },
  { id: "sqs", label: "SQS", color: "text-yellow-400" },
  { id: "sns", label: "SNS", color: "text-pink-400" },
  { id: "dynamodb", label: "DynamoDB", color: "text-blue-400" },
  { id: "lambda", label: "Lambda", color: "text-purple-400" },
] as const

type SourceId = (typeof SOURCES)[number]["id"]

// ─── Component ────────────────────────────────────────────────────────────────

export function EventsPage() {
  // null means "all sources"
  const [filter, setFilter] = useState<SourceId | null>(null)

  const { events, connected, clear } = useEventStream({
    source: filter ?? undefined,
  })

  return (
    <div className="flex w-full max-w-screen-xl flex-col gap-4">
      <PageHeader
        title="Event Stream"
        description="Live feed of all internal events. Filters apply from the moment you select them."
        actions={
          <div className="flex items-center gap-1">
            <Button
              variant={filter === null ? "secondary" : "ghost"}
              size="sm"
              onClick={() => setFilter(null)}
            >
              All
            </Button>
            {SOURCES.map((s) => (
              <Button
                key={s.id}
                variant={filter === s.id ? "secondary" : "ghost"}
                size="sm"
                onClick={() => setFilter(filter === s.id ? null : s.id)}
                className={filter === s.id ? "" : s.color}
              >
                {s.label}
              </Button>
            ))}
          </div>
        }
      />

      <EventConsole events={events} connected={connected} onClear={clear} />
    </div>
  )
}

// ─── Empty state icon (re-used by route) ──────────────────────────────────────
export { Activity as EventsIcon }
