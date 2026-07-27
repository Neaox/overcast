import { GlobalSearchTrigger } from "@/components/layout/global-search"
import { useSidebarCollapse } from "../use-sidebar-collapse"
import { HeaderActions } from "./header-actions"
import { HeaderBrand } from "./header-brand"
import { HeaderBreadcrumbs } from "./header-breadcrumbs"

export function Header({ onSearchOpen }: { onSearchOpen: () => void }) {
  const { collapsed } = useSidebarCollapse()

  return (
    <header className="flex h-13 shrink-0 items-center gap-4 border-b border-border bg-bg-elevated px-4">
      {collapsed && <HeaderBrand />}
      <HeaderBreadcrumbs />
      <div className="flex min-w-0 flex-1 justify-center">
        <GlobalSearchTrigger onClick={onSearchOpen} />
      </div>
      <HeaderActions />
    </header>
  )
}
