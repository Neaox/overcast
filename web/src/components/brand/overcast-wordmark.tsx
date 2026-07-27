import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

const wordmarkVariants = cva("font-mono font-bold tracking-[-0.01em] text-fg", {
  variants: {
    size: {
      sm: "text-[14px]",
      md: "text-[15px]",
    },
  },
  defaultVariants: { size: "md" },
})

interface OvercastWordmarkProps extends VariantProps<typeof wordmarkVariants> {
  className?: string
}

/** Overcast wordmark — always lowercase. */
export function OvercastWordmark({ size, className }: OvercastWordmarkProps) {
  return <span className={cn(wordmarkVariants({ size }), className)}>overcast</span>
}
