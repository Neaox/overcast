import { logs } from "@/services/api"
import { logsKeys } from "@/features/cloudwatch/logs/data"
import type { LogGroup } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<LogGroup>({
  id: "cloudwatch-logs",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => logsKeys.groups(),
  fetchAll: () => logs.listGroups(),
  matchFields: (g) => [g.logGroupName ?? "", g.arn ?? ""],
  toResult: (g) => ({
    id: `logs:${g.logGroupName}`,
    label: g.logGroupName ?? "",
    sublabel: g.arn,
    service: "CloudWatch Logs",
    serviceKey: "/cloudwatch",
    type: "Log Group",
    href: `/cloudwatch/logs/${encodeURIComponent(g.logGroupName ?? "")}`,
  }),
})
