/**
 * The panel and row layout every section of the bucket Configuration tab uses.
 *
 * It lives in its own file rather than in bucket-config.tsx because the panels
 * that fill the tab are separate components, and importing the tab from one of
 * them would pull in its route module.
 */
import { Definition, DefinitionList } from "@/components/ui/definition-card"

export function ConfigSection({
  title,
  icon,
  children,
}: {
  title: string
  icon: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        {icon}
        <h2 className="font-mono text-sm font-medium text-fg">{title}</h2>
      </div>
      <div className="flex flex-col divide-y divide-border overflow-hidden rounded-lg border border-border bg-bg-elevated">
        {children}
      </div>
    </div>
  )
}

/**
 * One labelled row inside a section. Always inline — these sit under a heading
 * where the label column is what ties the rows together, however wide the panel
 * gets.
 */
export function ConfigRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <DefinitionList layout="inline" className="pl-5">
      <Definition label={label} value={children} valueClassName="flex-1" />
    </DefinitionList>
  )
}
