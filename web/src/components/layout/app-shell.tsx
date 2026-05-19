import { useState, useEffect, useRef } from "react"
import { useRouterState } from "@tanstack/react-router"
import { Sidebar } from "./sidebar"
import { Header } from "./header"
import { OfflineBanner } from "./offline-banner"
import { ConnectionDialog } from "./connection-dialog"
import { GlobalSearch, useGlobalSearchShortcut } from "./global-search"
import { isConfigured } from "@/hooks/use-endpoint"
import { ConnectionStatusProvider } from "@/hooks/use-connection-status"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { useEventStreamSubscription } from "@/hooks/use-event-stream"

interface AppShellProps {
  children: React.ReactNode
}

export function AppShell({ children }: AppShellProps) {
  return (
    <ConnectionStatusProvider>
      <FavouritesProvider>
        <AppShellInner>{children}</AppShellInner>
      </FavouritesProvider>
    </ConnectionStatusProvider>
  )
}

function AppShellInner({ children }: AppShellProps) {
  const [connected, setConnected] = useState(() => isConfigured())
  const [searchOpen, setSearchOpen] = useState(false)
  const mainRef = useRef<HTMLElement>(null)
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  useGlobalSearchShortcut(() => setSearchOpen(true))

  // Single app-wide EventSource — all useEventStream consumers read from
  // the query cache, so only one SSE connection is ever open.
  // Query invalidation happens synchronously inside onMessage.
  useEventStreamSubscription()

  // Scroll the main content area back to the top whenever the route changes.
  // TanStack Router's built-in scroll reset targets window, but since <main>
  // owns its own scroll container the window scroll is always already at 0.
  useEffect(() => {
    mainRef.current?.scrollTo({ top: 0, behavior: "instant" })
  }, [pathname])

  if (!connected) {
    return <ConnectionDialog onConnected={() => setConnected(true)} />
  }

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <OfflineBanner />
        <Header onSearchOpen={() => setSearchOpen(true)} />
        <main ref={mainRef} className="flex-1 overflow-auto bg-bg p-4">
          {children}
        </main>
      </div>
      <GlobalSearch open={searchOpen} onOpenChange={setSearchOpen} />
    </div>
  )
}
