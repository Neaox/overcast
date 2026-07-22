import { beforeEach, describe, expect, it, vi } from "vitest"
import type React from "react"
import { InboxPage } from "@/features/mail/mail-page"
import { inboxMessagesQueryOptions } from "@/features/mail/data"
import { ToastContextProvider } from "@/components/ui/toast"
import { TooltipProvider } from "@/components/ui/tooltip"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { serverInfoQueryOptions } from "@/hooks/use-server-info"
import { createTestQueryClient, renderWithRouter, screen } from "@/test/render"
import { Sidebar } from "./sidebar"
import {
  NARROW_SIDEBAR_QUERY,
  SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY,
  SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY,
} from "./use-sidebar-collapse"
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

function mockNarrowViewport(isNarrow: boolean) {
  return vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
    matches: isNarrow && query === NARROW_SIDEBAR_QUERY,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }))
}

function SidebarOnly() {
  return (
    <TooltipProvider>
      <FavouritesProvider>
        <Sidebar />
      </FavouritesProvider>
    </TooltipProvider>
  )
}

function SidebarWithInbox() {
  return (
    <ToastContextProvider>
      <TooltipProvider>
        <FavouritesProvider>
          <div className="flex">
            <Sidebar />
            <InboxPage />
          </div>
        </FavouritesProvider>
      </TooltipProvider>
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

describe("Sidebar collapse state", () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it("starts collapsed in narrow viewports when no preference is saved", async () => {
    mockNarrowViewport(true)

    renderScreen(SidebarOnly)

    expect(await screen.findByRole("button", { name: "Expand sidebar" })).toBeInTheDocument()
  })

  it("persists the collapse state across refreshes", async () => {
    const { user, unmount } = renderScreen(SidebarOnly)

    await user.click(await screen.findByRole("button", { name: "Collapse" }))
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY)).toBe("true")
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY)).toBeNull()

    unmount()
    renderScreen(SidebarOnly)

    expect(await screen.findByRole("button", { name: "Expand sidebar" })).toBeInTheDocument()
  })

  it("stores narrow viewport preferences separately from wide preferences", async () => {
    const matchMedia = mockNarrowViewport(true)
    const { user, unmount } = renderScreen(SidebarOnly)

    await user.click(await screen.findByRole("button", { name: "Expand sidebar" }))
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_NARROW_STORAGE_KEY)).toBe("false")
    expect(localStorage.getItem(SIDEBAR_COLLAPSED_WIDE_STORAGE_KEY)).toBeNull()

    unmount()
    matchMedia.mockRestore()
    mockNarrowViewport(false)
    renderScreen(SidebarOnly)

    expect(await screen.findByRole("button", { name: "Collapse" })).toBeInTheDocument()
  })

  it("shows immediate UI tooltips for collapsed icon links", async () => {
    const { user } = renderScreen(SidebarOnly)

    await user.click(await screen.findByRole("button", { name: "Collapse" }))
    const dashboardLink = await screen.findByRole("link", { name: "Dashboard" })
    expect(dashboardLink).not.toHaveAttribute("title")

    await user.hover(dashboardLink)

    expect(await screen.findAllByText("Dashboard")).not.toHaveLength(0)
  })
})
