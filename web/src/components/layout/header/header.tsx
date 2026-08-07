import { GlobalSearchTrigger } from "@/components/layout/global-search"
import { useSidebarCollapse } from "../use-sidebar-collapse"
import { HeaderActions } from "./header-actions"
import { HeaderBrand } from "./header-brand"
import { HeaderBreadcrumbs } from "./header-breadcrumbs"
import { HeaderEndpoint } from "./header-endpoint"

export function Header({ onSearchOpen }: { onSearchOpen: () => void }) {
  const { collapsed } = useSidebarCollapse()

  return (
    <header className="@container flex h-13 shrink-0 items-center gap-3 border-b border-border bg-bg-elevated px-4">
      {collapsed && <HeaderBrand />}
      <HeaderEndpoint />
      <HeaderBreadcrumbs />
      <div className="flex min-w-0 flex-1 justify-center">
        <GlobalSearchTrigger onClick={onSearchOpen} />
      </div>
      <HeaderActions />
    </header>
  )
}
