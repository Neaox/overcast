import { useCallback, useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { CalendarClock } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { fieldLabel } from "@/lib/typography"
import { cn } from "@/lib/utils"

/**
 * Jump-to-timestamp — a toolbar control that repositions the view on the
 * first event at-or-after a chosen instant.
 *
 * It owns only the picking; the caller turns the chosen time into an anchor
 * (the same deep-link machinery a search result uses), so a jump costs
 * exactly what an anchored arrival costs and nothing more.
 */
export function JumpToTimestamp({ onJump }: { onJump: (timestamp: number) => void }) {
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState("")
  const triggerRef = useRef<HTMLButtonElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const [pos, setPos] = useState({ top: 0, right: 0 })

  const updatePosition = useCallback(() => {
    if (!triggerRef.current) return
    const rect = triggerRef.current.getBoundingClientRect()
    setPos({ top: rect.bottom + 4, right: Math.max(window.innerWidth - rect.right, 8) })
  }, [])

  const handleToggle = useCallback(() => {
    if (!open) updatePosition()
    setOpen(!open)
  }, [open, updatePosition])

  useEffect(() => {
    if (!open) return
    updatePosition()
    inputRef.current?.focus()
    function handleClick(e: MouseEvent) {
      const target = e.target as Node
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) return
      setOpen(false)
    }
    document.addEventListener("mousedown", handleClick)
    return () => document.removeEventListener("mousedown", handleClick)
  }, [open, updatePosition])

  const submit = useCallback(() => {
    if (!value) return
    const timestamp = new Date(value).getTime()
    if (Number.isNaN(timestamp)) return
    setOpen(false)
    onJump(timestamp)
  }, [value, onJump])

  return (
    <>
      <Button
        ref={triggerRef}
        size="sm"
        variant="ghost"
        onClick={handleToggle}
        className="h-7 px-2"
        aria-label="Jump to timestamp"
        title="Jump to timestamp — centre the view on the first event at or after a chosen time"
      >
        <CalendarClock className="h-3.5 w-3.5" />
      </Button>

      {open &&
        createPortal(
          <div
            ref={panelRef}
            style={{ position: "fixed", top: pos.top, right: pos.right, zIndex: 9999 }}
          >
            <div className="flex w-64 flex-col gap-2 rounded-lg border border-border bg-bg-elevated p-3 shadow-lg">
              <label className={cn(fieldLabel, "text-fg-muted")} htmlFor="jump-to-timestamp-input">
                Date and time
              </label>
              <Input
                id="jump-to-timestamp-input"
                ref={inputRef}
                type="datetime-local"
                step="1"
                className="h-8 text-sm"
                value={value}
                onChange={(e) => setValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") submit()
                  if (e.key === "Escape") setOpen(false)
                }}
              />
              <Button size="sm" className="h-8 w-full" disabled={!value} onClick={submit}>
                Jump
              </Button>
            </div>
          </div>,
          document.body,
        )}
    </>
  )
}
