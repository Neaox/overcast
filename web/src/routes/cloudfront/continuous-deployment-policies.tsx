import { createFileRoute } from "@tanstack/react-router"
import { ContinuousDeploymentPolicyList } from "@/features/cloudfront/components/continuous-deployment-policy-list"

export const Route = createFileRoute("/cloudfront/continuous-deployment-policies")({
  head: () => ({
    meta: [{ title: "Continuous Deployment Policies — CloudFront — Overcast" }],
  }),
  component: ContinuousDeploymentPolicyList,
})
