/**
 * JsonEditor — a live-editable textarea with PrismJS JSON syntax highlighting
 * and inline validation feedback. Zero external editor framework dependencies.
 *
 * Usage:
 *   <JsonEditor value={text} onChange={setText} error={parseError} />
 */
import _Editor from "react-simple-code-editor"
import Prism from "@/lib/prism"

/** CJS/ESM interop: unwraps `.default` when a bundler hands us the whole module object. */
function getDefaultExport<T>(mod: T): T {
  return typeof mod === "object" && mod !== null && "default" in mod ? (mod.default as T) : mod
}
const Editor = getDefaultExport(_Editor)
import { cn } from "@/lib/utils"
// Prism doesn't ship a default theme — we apply one inline below.

interface JsonEditorProps {
  value: string
  onChange: (value: string) => void
  /** If non-null, shown as an error banner below the editor */
  error?: string | null
  placeholder?: string
  minHeight?: number
  className?: string
}

export function JsonEditor({
  value,
  onChange,
  error,
  placeholder,
  minHeight = 180,
  className = "",
}: JsonEditorProps) {
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <div
        className={cn(
          "json-editor-wrap overflow-auto rounded-md border font-mono text-xs",
          error ? "border-danger" : "border-border",
          "focus-within:ring-ring bg-bg focus-within:ring-1",
        )}
        style={{ minHeight }}
      >
        <Editor
          value={value}
          onValueChange={onChange}
          highlight={(code) =>
            code
              ? Prism.highlight(code, Prism.languages.json, "json")
              : placeholder
                ? `<span class="json-placeholder">${placeholder}</span>`
                : ""
          }
          padding={10}
          style={{
            minHeight,
            caretColor: "var(--color-fg)",
            outline: "none",
          }}
          textareaClassName="json-editor-textarea"
          // Prevent the outer click handler on dialog rows etc. from firing.
          onClick={(e) => e.stopPropagation()}
        />
      </div>
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  )
}
