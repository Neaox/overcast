import { useServiceIconColor } from "@/hooks/use-service-icon-color"
import { Switch } from "@/components/ui/switch"

/**
 * Settings → Appearance.
 *
 * One switch: whether service icons render tinted with their categorical-
 * ramp colour (service-registry.ts) or the design system's neutral accent
 * treatment. Backed by ServiceIconColorProvider so every mounted surface —
 * sidebar, dashboard, global search — repaints the moment this flips, the
 * same context-provider pattern use-sidebar-collapse.tsx uses for the same
 * reason.
 */
export function AppearanceSection() {
  const { enabled, setEnabled } = useServiceIconColor()

  return (
    <label className="flex cursor-pointer items-center justify-between gap-4">
      <div>
        <p className="font-mono text-sm font-medium text-fg">Service icon colours</p>
        <p className="text-xs text-fg-muted">
          Tint each service&apos;s icon with its own colour in the sidebar, dashboard, and search
          — off shows the neutral, single-colour icon set.
        </p>
      </div>
      <Switch checked={enabled} onCheckedChange={setEnabled} />
    </label>
  )
}
