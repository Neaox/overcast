import { useState } from "react"
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

  useGlobalSearchShortcut(() => setSearchOpen(true))

  // Single app-wide EventSource — all useEventStream consumers read from
  // the query cache, so only one SSE connection is ever open.
  // Query invalidation happens synchronously inside onMessage.
  useEventStreamSubscription()

  // Scrolling <main> back to the top on a route change is the router's job now
  // (scrollToTopSelectors in main.tsx), not an effect here. An effect on the
  // pathname cannot tell a new navigation from going Back, and resetting on
  // both is what lost your place in the trace list; the router resets only
  // when it has no cached offset for that history entry.

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
          id="main-content"
          // Names this element to the router's scroll restoration, which reads
          // the attribute off the scroll event's target to decide what it is
          // caching an offset for. Without it the router would key the offset
          // by a generated nth-child selector, or track the window — which
          // never scrolls here. See main.tsx.
          data-scroll-restoration-id="app-main"
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
