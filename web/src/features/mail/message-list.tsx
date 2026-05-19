/**
 * MessageList — left-panel list of captured SMTP messages.
 *
 * Each row shows: sender, subject (truncated), body preview, and relative
 * timestamp. The selected message is highlighted. Click a row to select it.
 *
 * Messages that share a groupId (all deliveries from one SNS Publish call)
 * are collapsed into a thread row. Click the thread row to expand/collapse it.
 */
import { useState } from "react"
import { formatDistanceToNow } from "date-fns"
import { Mail, MessageSquare, Webhook, Bell, ChevronRight } from "lucide-react"
import { cn } from "@/lib/utils"
import { EmptyState } from "@/components/ui/primitives"
import type { CapturedMessage } from "@/types"

// ─── Props ─────────────────────────────────────────────────────────────────

interface MessageListProps {
  messages: CapturedMessage[]
  selectedId: string | null
  onSelect: (id: string) => void
}

// A display item is either a standalone message or a group of related messages.
type StandaloneItem = { type: "standalone"; message: CapturedMessage }
type GroupItem = { type: "group"; groupId: string; groupTopic: string; messages: CapturedMessage[] }
type ListItem = StandaloneItem | GroupItem

/** Build the ordered list of display items, preserving chronological order. */
function buildListItems(messages: CapturedMessage[]): ListItem[] {
  const items: ListItem[] = []
  const seenGroups = new Map<string, GroupItem>()

  for (const msg of messages) {
    if (msg.groupId) {
      const existing = seenGroups.get(msg.groupId)
      if (existing) {
        existing.messages.push(msg)
      } else {
        const item: GroupItem = {
          type: "group",
          groupId: msg.groupId,
          groupTopic: msg.groupTopic ?? msg.groupId,
          messages: [msg],
        }
        seenGroups.set(msg.groupId, item)
        items.push(item)
      }
    } else {
      items.push({ type: "standalone", message: msg })
    }
  }

  return items
}

// ─── Component ─────────────────────────────────────────────────────────────

export function MessageList({ messages, selectedId, onSelect }: MessageListProps) {
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())

  if (messages.length === 0) {
    return (
      <EmptyState
        icon={<Mail className="h-8 w-8" />}
        title="No messages yet"
        description="Outbound email and SMS from SNS, SES, and Cognito will appear here."
        className="h-full px-6"
      />
    )
  }

  const items = buildListItems(messages)

  const toggleGroup = (groupId: string) => {
    setExpandedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(groupId)) {
        next.delete(groupId)
      } else {
        next.add(groupId)
      }
      return next
    })
  }

  return (
    <ul className="divide-y divide-border overflow-y-auto">
      {items.map((item) => {
        if (item.type === "standalone") {
          return (
            <MessageRow
              key={item.message.id}
              message={item.message}
              selected={item.message.id === selectedId}
              onSelect={onSelect}
            />
          )
        }

        const expanded = expandedGroups.has(item.groupId)
        const containsSelected = item.messages.some((m) => m.id === selectedId)

        return (
          <li key={item.groupId}>
            {/* Thread header row */}
            <button
              onClick={() => toggleGroup(item.groupId)}
              className={cn(
                "w-full px-3 py-2.5 text-left transition-colors",
                "border-l-2",
                expanded
                  ? "border-accent/40"
                  : containsSelected
                    ? "border-accent bg-sidebar-item-active text-sidebar-item-active-fg"
                    : "hover:bg-surface-hover border-transparent hover:border-border",
              )}
            >
              <div className="flex items-center gap-1.5">
                <ChevronRight
                  className={cn(
                    "h-3.5 w-3.5 shrink-0 transition-transform",
                    containsSelected && !expanded ? "text-sidebar-item-active-fg/70" : "text-fg-muted",
                    expanded && "rotate-90",
                  )}
                />
                <span className={cn(
                  "flex-1 truncate text-sm font-semibold",
                  containsSelected && !expanded ? "text-sidebar-item-active-fg" : "text-fg",
                )}>
                  {item.groupTopic}
                </span>
                <span className={cn(
                  "shrink-0 text-xs",
                  containsSelected && !expanded ? "text-sidebar-item-active-fg/70" : "text-fg-subtle",
                )}>
                  {formatDistanceToNow(new Date(item.messages[0].receivedAt), { addSuffix: true })}
                </span>
                <span className={cn(
                  "shrink-0 rounded-full px-1.5 py-0.5 text-xs tabular-nums",
                  containsSelected && !expanded
                    ? "bg-sidebar-item-active-fg/20 text-sidebar-item-active-fg/80"
                    : "bg-surface-muted text-fg-subtle",
                )}>
                  {item.messages.length}
                </span>
              </div>
            </button>

            {/* Expanded thread children */}
            {expanded && (
              <ul className="border-l-2 border-accent/40">
                {item.messages.map((msg) => (
                  <MessageRow
                    key={msg.id}
                    message={msg}
                    selected={msg.id === selectedId}
                    onSelect={onSelect}
                    indent
                  />
                ))}
              </ul>
            )}
          </li>
        )
      })}
    </ul>
  )
}

// ─── Row ───────────────────────────────────────────────────────────────────

interface MessageRowProps {
  message: CapturedMessage
  selected: boolean
  onSelect: (id: string) => void
  indent?: boolean
}

function MessageRow({ message, selected, onSelect, indent }: MessageRowProps) {
  const kind = message.kind ?? "email"
  const preview = message.textBody.slice(0, 100).replace(/\s+/g, " ").trim()
  const relTime = formatDistanceToNow(new Date(message.receivedAt), { addSuffix: true })

  // Primary identifier depends on kind.
  const primary =
    kind === "sms"
      ? (message.to[0] ?? "(unknown)")
      : kind === "webhook" || kind === "push"
        ? (message.to[0] ?? "(unknown)")
        : message.from

  // Headline: subject for email, body/URL snippet for others.
  const title =
    kind === "email"
      ? (message.subject ?? "(no subject)")
      : kind === "sms"
        ? message.textBody.slice(0, 60) || "SMS message"
        : kind === "webhook"
          ? (message.to[0] ?? "Webhook delivery")
          : (message.to[0] ?? "Push notification")

  const KindIcon =
    kind === "sms" ? MessageSquare : kind === "webhook" ? Webhook : kind === "push" ? Bell : Mail

  return (
    <li>
      <button
        onClick={() => onSelect(message.id)}
        className={cn(
          "w-full text-left transition-colors",
          indent ? "px-3 py-2" : "px-4 py-3",
          selected
            ? "bg-sidebar-item-active text-sidebar-item-active-fg"
            : "hover:bg-surface-hover",
        )}
      >
        {/* Primary identifier + source badge + timestamp */}
        <div className="flex items-center justify-between gap-2">
          <span
            className={cn(
              "flex items-center gap-1.5 truncate text-xs font-medium",
              selected ? "text-sidebar-item-active-fg/80" : "text-fg-muted",
            )}
          >
            <KindIcon className="h-3 w-3 shrink-0 opacity-70" />
            <span className="truncate">{primary}</span>
            {message.source && (
              <span
                className={cn(
                  "shrink-0 rounded px-1 py-0.5 text-xs leading-none",
                  selected
                    ? "bg-sidebar-item-active-fg/15 text-sidebar-item-active-fg/70"
                    : "bg-surface-muted text-fg-subtle",
                )}
              >
                {message.source}
              </span>
            )}
          </span>
          <span
            className={cn(
              "shrink-0 text-xs",
              selected ? "text-sidebar-item-active-fg/70" : "text-fg-subtle",
            )}
          >
            {relTime}
          </span>
        </div>

        {/* Title: subject for email, body snippet for SMS */}
        <p
          className={cn(
            "mt-0.5 truncate text-sm font-semibold",
            selected ? "text-sidebar-item-active-fg" : "text-fg",
          )}
        >
          {title}
        </p>

        {/* Preview: email text only (not JSON), hidden for other kinds */}
        {kind === "email" && preview && !preview.startsWith("{") && !preview.startsWith("[") && (
          <p
            className={cn(
              "mt-0.5 truncate text-xs",
              selected ? "text-sidebar-item-active-fg/70" : "text-fg-muted",
            )}
          >
            {preview}
          </p>
        )}
      </button>
    </li>
  )
}
