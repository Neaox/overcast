/**
 * MessageDetail — right-panel view of a single captured SMTP message.
 *
 * Shows header metadata (From, To, Subject, Date) and the message body.
 * If the message has an HTML part it is rendered in a sandboxed iframe;
 * otherwise the plain text body is shown pre-formatted.
 *
 * A "Delete" button removes the message from the inbox and fires onDelete.
 * A "Copy" button copies the active tab content to the clipboard.
 * JSON payloads (webhook/push) are pretty-printed with syntax highlighting.
 */
import { useState, useCallback } from "react"
import { Trash2, Mail, MessageSquare, Webhook, Bell, Copy, Check, GitBranch } from "lucide-react"
import Prism from "@/lib/prism"
import { EmptyState } from "@/components/ui/primitives"
import { Button } from "@/components/ui/button"
import type { CapturedMessage } from "@/types"
import { cn } from "@/lib/utils"

// ─── Props ─────────────────────────────────────────────────────────────────

interface MessageDetailProps {
  message: CapturedMessage | null
  onDelete: (id: string) => void
  deleting?: boolean
}

// ─── Component ─────────────────────────────────────────────────────────────

export function MessageDetail({ message, onDelete, deleting }: MessageDetailProps) {
  const [tab, setTab] = useState<"plain" | "html" | "raw">("plain")
  const [copied, setCopied] = useState(false)

  // Reset tab and copy state whenever the selected message changes.
  const messageId = message?.id
  const [prevMessageId, setPrevMessageId] = useState(messageId)
  if (messageId !== prevMessageId) {
    setPrevMessageId(messageId)
    setTab("plain")
    setCopied(false)
  }

  const kind = message?.kind ?? "email"
  const isTextOnly = kind === "sms" || kind === "webhook" || kind === "push"
  const hasHtml = !!message?.htmlBody && !isTextOnly
  const hasRaw = !isTextOnly && !!message?.raw

  // Compute the effective tab, falling back to "plain" if the current tab
  // is not available for this message (e.g. switched from an HTML email to a
  // plain text one while on the "html" tab).
  const effectiveTab =
    tab === "html" && !hasHtml ? "plain" : tab === "raw" && !hasRaw ? "plain" : tab

  // Try to pretty-print the body as JSON (webhook/push payloads in particular).
  const prettyJSON = message ? tryPrettyJSON(message.textBody) : null

  // Copy the current tab's content to clipboard. When the payload tab shows
  // pretty-printed JSON, copy the formatted version rather than the raw body.
  const handleCopy = useCallback(() => {
    if (!message) return
    const content =
      effectiveTab === "raw"
        ? (message.raw ?? "")
        : effectiveTab === "html"
          ? (message.htmlBody ?? "")
          : (prettyJSON ?? message.textBody)
    void navigator.clipboard.writeText(content).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }, [effectiveTab, message, prettyJSON])

  if (!message) {
    return (
      <EmptyState
        icon={<Mail className="h-8 w-8" />}
        title="Select a message"
        description="Click a message on the left to read it here."
        className="h-full"
      />
    )
  }

  const KindIcon =
    kind === "sms" ? MessageSquare : kind === "webhook" ? Webhook : kind === "push" ? Bell : null

  const kindTitle =
    kind === "sms"
      ? "SMS Message"
      : kind === "webhook"
        ? "Webhook Delivery"
        : kind === "push"
          ? "Push Notification"
          : message.subject || "(no subject)"

  const tabLabel = isTextOnly ? "Payload" : "Plain Text"

  return (
    <div className="flex h-full flex-col overflow-hidden">
      {/* Header */}
      <div className="flex shrink-0 items-start justify-between gap-4 border-b border-border px-6 py-4">
        <div className="min-w-0 flex-1 space-y-1">
          <h2 className="flex items-center gap-2 truncate text-base font-semibold text-fg">
            {KindIcon && <KindIcon className="h-4 w-4 shrink-0 text-fg-muted" />}
            {kindTitle}
            {message.source && (
              <span className="bg-surface-muted shrink-0 rounded px-1.5 py-0.5 text-xs font-normal text-fg-subtle">
                {message.source}
              </span>
            )}
          </h2>
          {/* For SMS/push: show To (phone/device) prominently; for webhook: show destination URL */}
          {kind === "sms" ? (
            <MetaRow label="To" value={message.to.join(", ")} />
          ) : kind === "webhook" || kind === "push" ? (
            <MetaRow label="Endpoint" value={message.to.join(", ")} />
          ) : (
            <>
              <MetaRow label="From" value={message.from} />
              <MetaRow label="To" value={message.to.join(", ")} />
            </>
          )}
          <MetaRow
            label="Date"
            value={new Date(message.receivedAt).toLocaleString(undefined, {
              dateStyle: "medium",
              timeStyle: "short",
            })}
          />
          {message.groupTopic && (
            <div className="flex items-center gap-1 pt-0.5 text-xs text-fg-subtle">
              <GitBranch className="h-3 w-3 shrink-0" />
              <span className="truncate">Thread: {message.groupTopic}</span>
            </div>
          )}
        </div>

        <Button
          variant="ghost"
          size="icon"
          className="shrink-0 text-fg-muted hover:text-fg"
          title="Copy content"
          onClick={handleCopy}
        >
          {copied ? <Check className="h-4 w-4 text-success" /> : <Copy className="h-4 w-4" />}
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="shrink-0 text-fg-muted hover:text-danger"
          title="Delete message"
          disabled={deleting}
          onClick={() => onDelete(message.id)}
        >
          <Trash2 className="h-4 w-4" />
        </Button>
      </div>

      {/* Tab bar */}
      <div className="flex shrink-0 items-center gap-1 border-b border-border px-4 py-1">
        <TabButton active={effectiveTab === "plain"} onClick={() => setTab("plain")}>
          {tabLabel}
        </TabButton>
        {hasHtml && (
          <TabButton active={effectiveTab === "html"} onClick={() => setTab("html")}>
            HTML
          </TabButton>
        )}
        {hasRaw && (
          <TabButton active={effectiveTab === "raw"} onClick={() => setTab("raw")}>
            Raw
          </TabButton>
        )}
      </div>

      {/* Body */}
      <div className="min-h-0 flex-1 overflow-auto">
        {effectiveTab === "plain" && prettyJSON ? (
          <pre
            className="min-h-full p-6 font-mono text-sm whitespace-pre-wrap text-fg"
            dangerouslySetInnerHTML={{
              __html: Prism.highlight(prettyJSON, Prism.languages.json, "json"),
            }}
          />
        ) : effectiveTab === "plain" ? (
          <pre className="min-h-full p-6 font-mono text-sm wrap-break-word whitespace-pre-wrap text-fg">
            {message.textBody || <span className="text-fg-muted italic">(no plain text body)</span>}
          </pre>
        ) : null}

        {effectiveTab === "html" && hasHtml && (
          <iframe
            srcDoc={message.htmlBody}
            sandbox="allow-same-origin"
            className="h-full w-full border-0 bg-white"
            title="HTML email preview"
          />
        )}

        {effectiveTab === "raw" && hasRaw && (
          <pre className="min-h-full p-6 font-mono text-xs wrap-break-word whitespace-pre-wrap text-fg-muted">
            {message.raw}
          </pre>
        )}
      </div>
    </div>
  )
}

// ─── Helpers ───────────────────────────────────────────────────────────────

/** Returns a pretty-printed JSON string if text parses as JSON, else null. */
function tryPrettyJSON(text: string): string | null {
  if (!text) return null
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return null
  }
}

function MetaRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex gap-2 text-sm">
      <span className="w-10 shrink-0 text-fg-subtle">{label}</span>
      <span className="truncate text-fg-muted">{value}</span>
    </div>
  )
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      onClick={onClick}
      className={cn(
        "px-3 py-1.5 text-xs font-medium",
        active ? "border-b-2 border-accent text-fg" : "text-fg-muted hover:text-fg",
      )}
    >
      {children}
    </button>
  )
}
