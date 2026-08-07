import { useState } from "react"
import { Check, ChevronDown, Copy } from "lucide-react"
import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { useCopyToClipboard } from "@/hooks/use-clipboard"
import { cn } from "@/lib/utils"

export interface CopyUrlFormat {
  label: string
  value: string
  description?: string
}

export interface CopyUrlButtonProps {
  formats: CopyUrlFormat[]
  noun?: string
  defaultIndex?: number
  compact?: boolean
}

export function CopyUrlButton({ formats, noun, defaultIndex = 0, compact }: CopyUrlButtonProps) {
  const { copy, copied } = useCopyToClipboard()
  const [activeIdx, setActiveIdx] = useState(defaultIndex)

  if (formats.length === 0) return null

  const active = formats[Math.min(activeIdx, formats.length - 1)]

  if (compact) {
    return (
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button
            type="button"
            onClick={(e) => e.stopPropagation()}
            title="Copy URL"
            aria-label="Copy URL"
            className="inline-flex items-center justify-center h-7 w-7 rounded hover:bg-bg-muted transition-colors"
          >
            {copied ? (
              <Check aria-hidden className="h-3.5 w-3.5 text-success" />
            ) : (
              <Copy aria-hidden className="h-3.5 w-3.5" />
            )}
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="end"
            sideOffset={4}
            className="z-50 min-w-[14rem] rounded-md border border-border bg-bg-elevated p-1 shadow-lg"
            onCloseAutoFocus={(e) => e.preventDefault()}
          >
            {formats.map((f) => (
              <DropdownMenu.Item
                key={f.label}
                onSelect={() => copy(f.value, { noun: noun ?? f.label })}
                className="flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm text-fg outline-none hover:bg-accent hover:text-white focus:bg-accent focus:text-white"
              >
                <span className="flex-1 truncate font-mono text-xs">{f.value}</span>
                {f.description && (
                  <span className="shrink-0 text-xs opacity-60">{f.description}</span>
                )}
              </DropdownMenu.Item>
            ))}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    )
  }

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          title="Copy URL"
          className="flex items-center gap-0.5 rounded px-1 py-0.5 min-w-0 hover:bg-bg-muted transition-colors focus-visible:ring-2 focus-visible:ring-accent focus-visible:outline-none"
        >
          <span className="truncate font-mono text-xs text-fg-muted max-w-64">
            {active.value}
          </span>
          <ChevronDown className="h-3 w-3 shrink-0 text-fg-subtle" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          sideOffset={4}
          className="z-50 min-w-[14rem] rounded-md border border-border bg-bg-elevated p-1 shadow-lg"
          onCloseAutoFocus={(e) => e.preventDefault()}
        >
          {formats.map((f, i) => (
            <DropdownMenu.Item
              key={f.label}
              onSelect={() => {
                setActiveIdx(i)
                copy(f.value, { noun: noun ?? f.label })
              }}
              className={cn(
                "flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm text-fg outline-none hover:bg-accent hover:text-white focus:bg-accent focus:text-white",
                i === activeIdx && "font-medium",
              )}
            >
              <Check className={cn("h-3.5 w-3.5 shrink-0", i === activeIdx ? "opacity-100" : "opacity-0")} />
              <span className="flex-1 truncate font-mono text-xs">{f.value}</span>
              {f.description && (
                <span className="shrink-0 text-xs opacity-60">{f.description}</span>
              )}
            </DropdownMenu.Item>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
