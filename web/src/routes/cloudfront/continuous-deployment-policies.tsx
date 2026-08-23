import { createFileRoute } from "@tanstack/react-router"
import { ContinuousDeploymentPolicyList } from "@/features/cloudfront/components/continuous-deployment-policy-list"
import { useSortSearchParam } from "@/hooks/use-sort-search-param"

type ContinuousDeploymentPoliciesSearch = {
  /** Table sort — `id` ascending, `-id` descending, deep-linkable. See `useSortSearchParam`. */
  sort?: string
}

export const Route = createFileRoute("/cloudfront/continuous-deployment-policies")({
  head: () => ({
    meta: [{ title: "Continuous Deployment Policies — CloudFront — Overcast" }],
  }),
  validateSearch: (search: Record<string, unknown>): ContinuousDeploymentPoliciesSearch => ({
    sort: typeof search.sort === "string" ? search.sort : undefined,
  }),
  component: function ContinuousDeploymentPoliciesRoute() {
    const [sort, setSort] = useSortSearchParam(Route.useSearch(), Route.useNavigate())
    return <ContinuousDeploymentPolicyList sort={sort} onSortChange={setSort} />
  },
})
