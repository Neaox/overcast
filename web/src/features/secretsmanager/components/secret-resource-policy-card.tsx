/**
 * The secret's resource policy.
 *
 * The "stored, not enforced" line is not a disclaimer to be tidied away later:
 * Overcast keeps and validates the policy but nothing evaluates it, so a reader
 * who assumes this page is showing them access control in force would be wrong
 * about their own stack. See docs/services/secretsmanager.md.
 */

import { Badge } from "@/components/ui/badge"
import { CodeBlock, SectionLabel } from "@/components/ui/primitives"

interface Props {
  policy?: string | null
}

function prettyPrint(policy: string): string {
  try {
    return JSON.stringify(JSON.parse(policy), null, 2)
  } catch {
    return policy
  }
}

export function SecretResourcePolicyCard({ policy }: Props) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <SectionLabel>Resource policy</SectionLabel>
        <Badge variant="warning">Stored, not enforced</Badge>
      </div>
      <p className="text-xs text-fg-muted">
        Overcast stores and validates this policy but does not evaluate it — it grants and denies
        nothing here.
      </p>
      {policy ? (
        <CodeBlock className="max-h-96 overflow-y-auto text-xs">{prettyPrint(policy)}</CodeBlock>
      ) : (
        <div className="rounded-md border border-border px-4 py-3 text-sm text-fg-muted">
          No resource policy attached.
        </div>
      )}
    </section>
  )
}
