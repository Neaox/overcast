import { useState } from "react"
import { Sidebar } from "./sidebar"
import { Header } from "./header"
import { ConnectionDialog } from "./connection-dialog"
import { EndpointProvider, isConfigured } from "@/hooks/use-endpoint"

interface AppShellProps {
  children: React.ReactNode
}

export function AppShell({ children }: AppShellProps) {
  return (
    <EndpointProvider>
      <AppShellInner>{children}</AppShellInner>
    </EndpointProvider>
  )
}

function AppShellInner({ children }: AppShellProps) {
  const [connected, setConnected] = useState(() => isConfigured())

  if (!connected) {
    return <ConnectionDialog onConnected={() => setConnected(true)} />
  }

  return (
    <div className="flex h-full overflow-hidden">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <Header />
        <main className="flex-1 overflow-auto bg-bg p-4">{children}</main>
      </div>
    </div>
  )
}
