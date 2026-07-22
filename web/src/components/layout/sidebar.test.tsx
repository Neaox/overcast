import { beforeEach, describe, expect, it } from "vitest"
import type React from "react"
import { InboxPage } from "@/features/mail/mail-page"
import { inboxMessagesQueryOptions } from "@/features/mail/data"
import { ToastContextProvider } from "@/components/ui/toast"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { serverInfoQueryOptions } from "@/hooks/use-server-info"
import { createTestQueryClient, renderWithRouter, screen } from "@/test/render"
import { Sidebar } from "./sidebar"
import type { CapturedMessage } from "@/types"

const messages: CapturedMessage[] = [
  {
    id: "msg-1",
    kind: "email",
    from: "sender@example.com",
    to: ["reader@example.com"],
    subject: "First unread",
    textBody: "Body one",
    receivedAt: "2026-05-30T12:00:00Z",
  },
  {
    id: "msg-2",
    kind: "sms",
    from: "+15550000000",
    to: ["+15551112222"],
    textBody: "Second unread",
    receivedAt: "2026-05-30T12:01:00Z",
  },
]

function renderScreen(component: React.FC, { debug = false }: { debug?: boolean } = {}) {
  const queryClient = createTestQueryClient()
  queryClient.setQueryData(inboxMessagesQueryOptions().queryKey, messages)
  queryClient.setQueryData(serverInfoQueryOptions().queryKey, { debug })

  return renderWithRouter(component, { queryClient })
}

function SidebarOnly() {
  return (
    <FavouritesProvider>
      <Sidebar />
    </FavouritesProvider>
  )
}

function SidebarWithInbox() {
  return (
    <ToastContextProvider>
      <FavouritesProvider>
        <div className="flex">
          <Sidebar />
          <InboxPage />
        </div>
      </FavouritesProvider>
    </ToastContextProvider>
  )
}

function InboxOnly() {
  return (
    <ToastContextProvider>
      <InboxPage />
    </ToastContextProvider>
  )
}

describe("Sidebar inbox badge", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("shows the unread inbox count", async () => {
    renderScreen(SidebarOnly)

    expect(await screen.findByLabelText("2 unread inbox messages")).toHaveTextContent("2")
  })

  it("updates when an inbox message is read", async () => {
    const { user } = renderScreen(SidebarWithInbox)

    await user.click(await screen.findByRole("button", { name: /First unread/ }))

    expect(await screen.findByLabelText("1 unread inbox message")).toHaveTextContent("1")
  })

  it("filters the inbox to unread messages", async () => {
    const { user } = renderScreen(InboxOnly)

    await user.click(await screen.findByRole("button", { name: /First unread/ }))
    await user.click(screen.getByRole("button", { name: /Second unread/ }))
    await user.click(screen.getByRole("button", { name: "Unread" }))

    expect(screen.queryByRole("button", { name: /First unread/ })).not.toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Second unread/ })).toBeInTheDocument()
  })

  it("keeps the selected message visible while filtering unread messages", async () => {
    const { user } = renderScreen(InboxOnly)

    await user.click(await screen.findByRole("button", { name: "Unread 2" }))
    await user.click(screen.getByRole("button", { name: /First unread/ }))

    expect(screen.getByRole("button", { name: /First unread/ })).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /Second unread/ })).toBeInTheDocument()
  })
})

describe("Sidebar debug navigation", () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it("hides the debug link when debug mode is disabled", async () => {
    renderScreen(SidebarOnly)

    await screen.findByRole("link", { name: "Dashboard" })
    expect(screen.queryByRole("link", { name: "Debug" })).not.toBeInTheDocument()
  })

  it("shows the debug link when debug mode is enabled", async () => {
    renderScreen(SidebarOnly, { debug: true })

    expect(await screen.findByRole("link", { name: "Debug" })).toBeInTheDocument()
  })
})
