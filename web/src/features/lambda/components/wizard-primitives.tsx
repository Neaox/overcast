/**
 * Shared primitives for Lambda multi-step wizards.
 *
 * Extracted from CreateFunctionWizard and PublishLayerWizard.
 * Used whenever two or more wizards share the same UI pattern.
 */
import { useRef } from "react"
import { Upload, Check } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { FormField } from "@/components/ui/form"
import { cn } from "@/lib/utils"

// ─── StepDot ─────────────────────────────────────────────────────────────────

export function StepDot({
  label,
  active,
  done,
}: {
  label: string
  active: boolean
  done: boolean
}) {
  return (
    <div className="flex items-center gap-1.5">
      <div
        className={cn(
          "flex h-5 w-5 items-center justify-center rounded-full text-[10px] font-bold",
          done
            ? "bg-accent text-fg-on-accent"
            : active
              ? "border-2 border-accent text-accent"
              : "border border-border text-fg-muted",
        )}
      >
        {done ? <Check className="h-3 w-3" /> : null}
      </div>
      <span className={cn("text-xs", active || done ? "font-medium text-fg" : "text-fg-muted")}>
        {label}
      </span>
    </div>
  )
}

// ─── WizardOptionCard ─────────────────────────────────────────────────────────
//
// Card-style selector button used across wizard step headers.
// Renders as a stacked column: icon → label → optional description.

export function WizardOptionCard({
  active,
  icon,
  label,
  description,
  onClick,
}: {
  active: boolean
  icon: React.ReactNode
  label: string
  description?: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex flex-1 flex-col gap-1 rounded-md border px-3 py-2.5 text-left text-sm transition-colors",
        active
          ? "border-accent bg-accent/10 text-accent"
          : "border-border text-fg-muted hover:border-fg-subtle",
      )}
    >
      <span className="flex items-center gap-2 font-medium">
        {icon}
        {label}
      </span>
      {description && <span className="text-xs opacity-70">{description}</span>}
    </button>
  )
}

// ─── ZipDropzone ─────────────────────────────────────────────────────────────

export function ZipDropzone({
  file,
  onChange,
  description = "Select a .zip file",
}: {
  file: File | null
  onChange: (f: File) => void
  description?: string
}) {
  const inputRef = useRef<HTMLInputElement>(null)

  return (
    <div className="flex flex-col items-center gap-3 rounded-md border border-dashed border-border py-10">
      <Upload className="h-8 w-8 text-fg-subtle" />
      <p className="text-sm text-fg-muted">
        {file ? (
          <>
            <span className="font-medium text-fg">{file.name}</span>
            <span className="ml-1">({(file.size / 1024).toFixed(1)} KB)</span>
          </>
        ) : (
          description
        )}
      </p>
      <input
        ref={inputRef}
        type="file"
        accept=".zip,application/zip"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f) onChange(f)
        }}
      />
      <Button variant="secondary" size="sm" type="button" onClick={() => inputRef.current?.click()}>
        {file ? "Change file" : "Choose file"}
      </Button>
    </div>
  )
}

// ─── ZipS3SourceFields ────────────────────────────────────────────────────────

export function ZipS3SourceFields({
  bucket,
  onBucket,
  s3Key,
  onS3Key,
  objectVersion,
  onObjectVersion,
  bucketPlaceholder = "my-deployment-bucket",
  keyPlaceholder = "path/to/package.zip",
}: {
  bucket: string
  onBucket: (v: string) => void
  s3Key: string
  onS3Key: (v: string) => void
  objectVersion: string
  onObjectVersion: (v: string) => void
  bucketPlaceholder?: string
  keyPlaceholder?: string
}) {
  return (
    <div className="flex flex-col gap-3">
      <FormField label="S3 bucket">
        <Input
          value={bucket}
          onChange={(e) => onBucket(e.target.value)}
          placeholder={bucketPlaceholder}
          autoFocus
        />
      </FormField>
      <FormField label="S3 key">
        <Input
          value={s3Key}
          onChange={(e) => onS3Key(e.target.value)}
          placeholder={keyPlaceholder}
        />
      </FormField>
      <FormField label="S3 object version" hint="Optional">
        <Input
          value={objectVersion}
          onChange={(e) => onObjectVersion(e.target.value)}
          placeholder="aBcDeFgH…"
        />
      </FormField>
    </div>
  )
}

// ─── readZipAsBase64 ──────────────────────────────────────────────────────────

// eslint-disable-next-line react-refresh/only-export-components
export function readZipAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = reader.result as string
      resolve(result.split(",")[1] ?? "")
    }
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}
