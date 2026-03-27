/**
 * RegionSelect — combobox for picking an AWS region.
 *
 * Thin wrapper around <Combobox> / <ComboboxCompact> with AWS region data baked in.
 *
 * Usage:
 *   <RegionSelect value={region} onChange={setRegion} />
 *   <RegionSelectCompact value={region} onChange={setRegion} />
 */
import { Check } from "lucide-react"
import { cn } from "@/lib/utils"
import { Combobox, ComboboxCompact, type ComboboxRenderContext } from "./combobox"

// ─── Data ────────────────────────────────────────────────────────────────────

interface Region {
  code: string
  name: string
}

export const AWS_REGIONS: Region[] = [
  { code: "af-south-1", name: "Cape Town" },
  { code: "ap-east-1", name: "Hong Kong" },
  { code: "ap-northeast-1", name: "Tokyo" },
  { code: "ap-northeast-2", name: "Seoul" },
  { code: "ap-northeast-3", name: "Osaka" },
  { code: "ap-south-1", name: "Mumbai" },
  { code: "ap-south-2", name: "Hyderabad" },
  { code: "ap-southeast-1", name: "Singapore" },
  { code: "ap-southeast-2", name: "Sydney" },
  { code: "ap-southeast-3", name: "Jakarta" },
  { code: "ap-southeast-4", name: "Melbourne" },
  { code: "ca-central-1", name: "Canada (Central)" },
  { code: "ca-west-1", name: "Calgary" },
  { code: "eu-central-1", name: "Frankfurt" },
  { code: "eu-central-2", name: "Zurich" },
  { code: "eu-north-1", name: "Stockholm" },
  { code: "eu-south-1", name: "Milan" },
  { code: "eu-south-2", name: "Spain" },
  { code: "eu-west-1", name: "Ireland" },
  { code: "eu-west-2", name: "London" },
  { code: "eu-west-3", name: "Paris" },
  { code: "il-central-1", name: "Tel Aviv" },
  { code: "me-central-1", name: "UAE" },
  { code: "me-south-1", name: "Bahrain" },
  { code: "sa-east-1", name: "São Paulo" },
  { code: "us-east-1", name: "N. Virginia" },
  { code: "us-east-2", name: "Ohio" },
  { code: "us-gov-east-1", name: "GovCloud (US-East)" },
  { code: "us-gov-west-1", name: "GovCloud (US-West)" },
  { code: "us-west-1", name: "N. California" },
  { code: "us-west-2", name: "Oregon" },
]

export const AWS_REGION_CODES = AWS_REGIONS.map((r) => r.code)

// ─── Shared helpers ───────────────────────────────────────────────────────────

function regionFilter(region: Region, query: string) {
  const q = query.toLowerCase()
  return region.code.includes(q) || region.name.toLowerCase().includes(q)
}

function renderRegion(region: Region, { selected, active }: ComboboxRenderContext) {
  return (
    <span className="flex items-center gap-2">
      <Check className={cn("h-3.5 w-3.5 shrink-0", selected ? "opacity-100" : "opacity-0")} />
      <span className="font-mono text-sm">{region.code}</span>
      <span className={cn("ml-auto text-sm", active ? "text-white/70" : "text-fg-muted")}>
        {region.name}
      </span>
    </span>
  )
}

function renderCustomFooter(query: string, select: (v: string) => void) {
  return (
    <div className="border-t border-border px-3 py-1.5">
      <button
        onMouseDown={(e) => {
          e.preventDefault()
          select(query)
        }}
        className="w-full text-left text-sm text-fg-muted hover:text-fg"
      >
        Use custom region <span className="font-mono text-fg">"{query}"</span>
      </button>
    </div>
  )
}

// ─── Components ───────────────────────────────────────────────────────────────

export interface RegionSelectProps {
  value: string
  onChange: (region: string) => void
  id?: string
  className?: string
}

export function RegionSelect({ value, onChange, id, className }: RegionSelectProps) {
  return (
    <Combobox
      value={value}
      onChange={onChange}
      items={AWS_REGIONS}
      filterFn={regionFilter}
      getItemValue={(r) => r.code}
      renderItem={renderRegion}
      renderCustomFooter={renderCustomFooter}
      allowCustom
      id={id}
      placeholder="us-east-1"
      className={className}
      inputClassName="font-mono"
      popoverWidth="w-72"
    />
  )
}

export function RegionSelectCompact({
  value,
  onChange,
}: Pick<RegionSelectProps, "value" | "onChange">) {
  return (
    <ComboboxCompact
      value={value}
      onChange={onChange}
      items={AWS_REGIONS}
      filterFn={regionFilter}
      getItemValue={(r) => r.code}
      renderItem={renderRegion}
      renderCustomFooter={renderCustomFooter}
      allowCustom
      popoverWidth="w-72"
    />
  )
}
