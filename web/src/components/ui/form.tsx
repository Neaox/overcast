import * as React from "react"
import { cn } from "@/lib/utils"

// ─── FormField — label + input + optional error message ───────────────────
interface FormFieldProps {
  label: string
  htmlFor?: string
  error?: string
  hint?: string
  required?: boolean
  children: React.ReactNode
  className?: string
}

function FormField({ label, htmlFor, error, hint, required, children, className }: FormFieldProps) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </label>
      {children}
      {hint && !error && <p className="text-sm text-fg-subtle">{hint}</p>}
      {error && <p className="text-sm text-danger">{error}</p>}
    </div>
  )
}

// ─── FormRow — horizontal layout for a set of form fields ─────────────────
function FormRow({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex gap-3 [&>*]:flex-1", className)} {...props} />
}

export { FormField, FormRow }
