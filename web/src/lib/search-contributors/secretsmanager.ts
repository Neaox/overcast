import { secretsmanager } from "@/services/api"
import { smKeys } from "@/features/secretsmanager/data"
import type { SecretSummary } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SecretSummary>({
  id: "secretsmanager",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => smKeys.secrets(),
  fetchAll: () => secretsmanager.listSecrets(),
  matchFields: (s) => [s.Name, s.ARN, s.Description],
  toResult: (s) => ({
    id: `secretsmanager:${s.Name}`,
    label: s.Name ?? "",
    sublabel: s.ARN,
    service: "Secrets Manager",
    serviceKey: "/secretsmanager",
    type: "Secret",
    href: `/secretsmanager/${encodeURIComponent(s.Name ?? "")}`,
  }),
})
