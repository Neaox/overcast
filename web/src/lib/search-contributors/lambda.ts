import { lambda } from "@/services/api"
import type { LambdaFunction } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<LambdaFunction>({
  id: "lambda",
  cacheKey: (ep) => ["lambda", "functions", ep.baseUrl],
  fetchAll: () => lambda.listFunctions(),
  matchFields: (f) => [f.FunctionName ?? "", f.FunctionArn ?? "", f.Description ?? ""],
  toResult: (f) => ({
    id: `lambda:${f.FunctionName}`,
    label: f.FunctionName ?? "",
    sublabel: f.FunctionArn,
    service: "Lambda",
    serviceKey: "/lambda",
    type: "Function",
    href: `/lambda/${encodeURIComponent(f.FunctionName ?? "")}`,
  }),
})
