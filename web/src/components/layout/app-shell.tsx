import { useState, useEffect, useRef } from "react"
import { useRouterState } from "@tanstack/react-router"
import { Sidebar } from "./sidebar/sidebar"
import { Header } from "./header/header"
import { OfflineBanner } from "./offline-banner"
import { ConnectionGate } from "./connection-gate"
import { GlobalSearch, useGlobalSearchShortcut } from "./global-search"
import { ConnectionStatusProvider } from "@/hooks/use-connection-status"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { useEventStreamSubscription } from "@/hooks/use-event-stream"
import { SidebarCollapseProvider } from "./use-sidebar-collapse"

interface AppShellProps {
  children: React.ReactNode
}

export function AppShell({ children }: AppShellProps) {
  return (
    <ConnectionStatusProvider>
      <FavouritesProvider>
        <SidebarCollapseProvider>
          {/* Nothing behind the gate mounts — no SSE, no queries — until the
              emulator has actually answered. */}
          <ConnectionGate>
            <AppShellInner>{children}</AppShellInner>
          </ConnectionGate>
        </SidebarCollapseProvider>
      </FavouritesProvider>
    </ConnectionStatusProvider>
  )
}

function AppShellInner({ children }: AppShellProps) {
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

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <OfflineBanner />
        <Header onSearchOpen={() => setSearchOpen(true)} />
        <main ref={mainRef} className="flex-1 overflow-auto bg-bg p-6">
          {children}
        </main>
      </div>
      <GlobalSearch open={searchOpen} onOpenChange={setSearchOpen} />
    </div>
  )
}
