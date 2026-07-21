import { secretsmanager } from "@/services/api"
import type { SecretSummary } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SecretSummary>({
  id: "secretsmanager",
  cacheKey: (ep) => ["secretsmanager", "secrets", ep.baseUrl],
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
