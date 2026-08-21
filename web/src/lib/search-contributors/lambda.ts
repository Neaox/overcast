import { lambda } from "@/services/api"
import { lambdaKeys } from "@/features/lambda/data"
import type { LambdaFunction } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<LambdaFunction>({
  id: "lambda",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => lambdaKeys.functions(),
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
