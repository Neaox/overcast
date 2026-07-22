import * as TooltipPrimitive from "@radix-ui/react-tooltip"

function TooltipProvider({ children }: { children: React.ReactNode }) {
  return <TooltipPrimitive.Provider delayDuration={300}>{children}</TooltipPrimitive.Provider>
}

function Tooltip({
  children,
  content,
  side = "top",
  delayDuration,
}: {
  children: React.ReactNode
  content: React.ReactNode
  side?: "top" | "right" | "bottom" | "left"
  delayDuration?: number
}) {
  return (
    <TooltipPrimitive.Root delayDuration={delayDuration}>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <TooltipPrimitive.Portal>
        <TooltipPrimitive.Content
          side={side}
          sideOffset={4}
          className="animate-in fade-in-0 zoom-in-95 z-50 max-w-xs rounded-md border border-border bg-bg-elevated px-3 py-2 text-xs text-fg shadow-lg"
        >
          {content}
          <TooltipPrimitive.Arrow className="fill-border" />
        </TooltipPrimitive.Content>
      </TooltipPrimitive.Portal>
    </TooltipPrimitive.Root>
  )
}

export { Tooltip, TooltipProvider }
