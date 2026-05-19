import * as React from "react"
import type { AnyFieldMeta } from "@tanstack/form-core"
import { cn } from "@/lib/utils"

// ─── fieldError — extract the first error message from a TanStack Form field ─
//
// Usage (always show):   error={fieldError(field.state.meta.errors)}
// Usage (after touch):   error={fieldError(field.state.meta.errors, field.state.meta.isTouched)}
// eslint-disable-next-line react-refresh/only-export-components
export function fieldError(errors: unknown[], isTouched?: boolean): string | undefined {
  if (isTouched === false) return undefined
  const e = errors[0]
  if (!e) return undefined
  if (typeof e === "string") return e
  if (typeof e === "object" && "message" in e) {
    return (e as { message: string }).message
  }
  return undefined
}

// ─── Field — TanStack Form field wrapper ──────────────────────────────────
//
// Combines label + child component + error message, driven directly from the
// field context object returned by TanStack Form's render prop.
//
// Usage:
//   <form.Field name="email">
//     {(field) => (
//       <Field field={field} label="Email" hint="We'll never share it.">
//         <Input value={field.state.value} onChange={...} onBlur={field.handleBlur} />
//       </Field>
//     )}
//   </form.Field>
interface FieldProps {
  /** The field context from TanStack Form's render prop: `{(field) => ...}` */
  field: { state: { meta: AnyFieldMeta } }
  label: string
  hint?: string
  required?: boolean
  /** Only show the error after the field has been touched (default: true). */
  showErrorOnTouch?: boolean
  children: React.ReactNode
  className?: string
}

export function Field({
  field,
  label,
  hint,
  required,
  showErrorOnTouch = true,
  children,
  className,
}: FieldProps) {
  const { errors, isTouched } = field.state.meta
  const error = fieldError(errors, showErrorOnTouch ? isTouched : undefined)
  return (
    <FormField label={label} hint={hint} required={required} error={error} className={className}>
      {children}
    </FormField>
  )
}

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
  return <div className={cn("flex gap-3 *:flex-1", className)} {...props} />
}

export { FormField, FormRow }
