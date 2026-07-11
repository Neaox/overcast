import { inbox } from "@/services/api"
import type { CapturedMessage } from "@/types"
import { createSearchContributor } from "./create-contributor"

function messageTitle(message: CapturedMessage): string {
  const kind = message.kind ?? "email"
  if (kind === "email") return message.subject ?? "(no subject)"
  if (kind === "sms") return message.textBody.slice(0, 60) || "SMS message"
  return message.to[0] ?? (kind === "webhook" ? "Webhook delivery" : "Push notification")
}

function messageType(message: CapturedMessage): string {
  const kind = message.kind ?? "email"
  if (kind === "sms") return "SMS"
  if (kind === "webhook") return "Webhook"
  if (kind === "push") return "Push"
  return "Email"
}

createSearchContributor<CapturedMessage>({
  id: "inbox",
  cacheKey: (ep) => [ep.baseUrl, ep.region, "inbox", "messages"],
  fetchAll: () => inbox.list(),
  matchFields: (message) => [
    messageTitle(message),
    message.from,
    ...message.to,
    message.textBody,
    message.source,
    message.groupTopic,
  ],
  toResult: (message) => ({
    id: `inbox:${message.id}`,
    label: messageTitle(message),
    sublabel: [message.from, message.to.join(", ")].filter(Boolean).join(" -> "),
    service: "Inbox",
    serviceKey: "/inbox",
    type: messageType(message),
    href: "/inbox",
  }),
})
