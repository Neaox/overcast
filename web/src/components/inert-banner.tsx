import { Info } from "lucide-react"

interface InertBannerProps {
  serviceName: string
}

/**
 * Banner displayed at the top of inert service pages to explain that the
 * service stores resources but does not enforce behaviour or side-effects.
 */
export function InertBanner({ serviceName }: InertBannerProps) {
  return (
    <div className="flex items-start gap-3 rounded-lg border border-sky-400/30 bg-sky-400/5 px-4 py-3 text-sm text-sky-300">
      <Info className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <span className="font-medium">{serviceName} is an inert service.</span> You can create,
        view, and delete resources, but no underlying behaviour will be enforced. For example,
        policies won&apos;t restrict access and workflows won&apos;t execute.
      </div>
    </div>
  )
}
