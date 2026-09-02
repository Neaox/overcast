import { useState, useEffect, useRef } from "react"
import { useRouterState } from "@tanstack/react-router"
import { Sidebar } from "./sidebar/sidebar"
import { Header } from "./header/header"
import { ConnectionToast } from "./connection-toast"
import { ConnectionGate } from "./connection-gate"
import { GlobalSearch, useGlobalSearchShortcut } from "./global-search"
import { ServiceFavicon } from "./service-favicon"
import { ConnectionStatusProvider } from "@/hooks/use-connection-status"
import { FavouritesProvider } from "@/hooks/use-favourites"
import { useEventStreamSubscription } from "@/hooks/use-event-stream"
import { ServiceIconColorProvider } from "@/hooks/use-service-icon-color"
import { SidebarCollapseProvider } from "./use-sidebar-collapse"

interface AppShellProps {
  children: React.ReactNode
}

export function AppShell({ children }: AppShellProps) {
  return (
    <ConnectionStatusProvider>
      <ServiceFavicon />
      <ServiceIconColorProvider>
        <SidebarCollapseProvider>
          {/* Nothing behind the gate mounts — no SSE, no queries — until the
              emulator has actually answered. FavouritesProvider reads the
              emulator's enabled services, so it belongs behind the gate too. */}
          <ConnectionGate>
            <FavouritesProvider>
              <AppShellInner>{children}</AppShellInner>
            </FavouritesProvider>
          </ConnectionGate>
        </SidebarCollapseProvider>
      </ServiceIconColorProvider>
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
      {/* The first tab stop on every page. The sidebar is forty-odd links deep, so
          without this a keyboard user walks the whole service list to reach the content
          again after every navigation. `tabIndex={-1}` on <main> is what makes the jump
          move focus rather than only the viewport. */}
      <a
        href="#main-content"
        className="sr-only font-mono text-xs focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded-control focus:border focus:border-accent focus:bg-bg-elevated focus:px-3 focus:py-2 focus:text-accent focus:shadow-sm"
      >
        Skip to content
      </a>
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header onSearchOpen={() => setSearchOpen(true)} />
        <main
          ref={mainRef}
          id="main-content"
          tabIndex={-1}
          className="flex-1 overflow-auto bg-bg p-6 focus:outline-none"
        >
          {children}
        </main>
      </div>
      <GlobalSearch open={searchOpen} onOpenChange={setSearchOpen} />
      {/* Docks itself bottom-right rather than taking a row off the shell. */}
      <ConnectionToast />
    </div>
  )
}
