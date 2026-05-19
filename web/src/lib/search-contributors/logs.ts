import { logs } from "@/services/api"
import type { LogGroup } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<LogGroup>({
  id: "cloudwatch-logs",
  // Log group keys don't include baseUrl — see logsKeys in cloudwatch/data.ts
  cacheKey: () => ["logs", "groups"],
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
