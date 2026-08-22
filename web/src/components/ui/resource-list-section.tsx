import * as React from "react"
import { cn } from "@/lib/utils"

/**
 * `ResourceListPage`'s body, without the page header — for a tabbed dashboard
 * where each tab is itself an Archetype A list (EventBridge's buses/rules,
 * IAM's users/roles/policies/groups, EC2's instances/VPCs/security groups/
 * elastic IPs/NAT gateways). Composed once per tab, inside `<TabPanel>`:
 *
 * ```tsx
 * <TabPanel id="buses">
 *   <ResourceListSection
 *     actions={
 *       <>
 *         <ResourceListFilter value={filter} onChange={setFilter} placeholder="Filter buses…" className="flex-1" />
 *         <RefreshAction isFetching={isFetching} onClick={() => refetch()} />
 *         <CreateAction onClick={() => setShowCreate(true)}>Create Bus</CreateAction>
 *       </>
 *     }
 *   >
 *     <ResourceTable
 *       variant="embedded"
 *       query={{ data: filtered, isLoading, error }}
 *       noun="buses"
 *       …
 *     />
 *   </ResourceListSection>
 * </TabPanel>
 * ```
 *
 * `actions` renders as one row above the body — the tab-local equivalent of
 * `ResourceListPage`'s header actions, minus a title (the tab strip already
 * names the section). `children` is deliberately not typed as "must be a
 * `ResourceTable`" — a panel that needs an extra control row (EC2's instance
 * state-filter chips) or content below the table (a plan's expanded key list)
 * passes it all as `children` in the order it should render.
 *
 * The table inside almost always wants `variant="embedded"`: the tab panel
 * is not its own card, so a nested `ResourceListCard` border would double up
 * against the tab strip's own edge.
 */
interface ResourceListSectionProps {
  /** The control row rendered above `children` — filter, refresh, create. Omitted entirely if not passed. */
  actions?: React.ReactNode
  children: React.ReactNode
  className?: string
}

function ResourceListSection({ actions, children, className }: ResourceListSectionProps) {
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
      {children}
    </div>
  )
}

export { ResourceListSection }
