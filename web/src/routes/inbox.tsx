import { createFileRoute } from "@tanstack/react-router"
import { InboxPage } from "@/features/mail/mail-page"

export const Route = createFileRoute("/inbox")({
  head: () => ({ meta: [{ title: "Inbox — Overcast" }] }),
  component: InboxPage,
})
