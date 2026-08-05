import { createFileRoute } from "@tanstack/react-router"
import { WebACLDetail } from "@/features/waf/components/web-acl-detail"

export const Route = createFileRoute("/waf/$scope/$webAclId/$name")({
  head: ({ params }) => ({ meta: [{ title: `${params.name} — WAF — Overcast` }] }),
  component: function WebACLDetailRoute() {
    const { scope, webAclId, name } = Route.useParams()
    if (scope !== "REGIONAL" && scope !== "CLOUDFRONT") {
      return <p className="p-6 text-sm text-fg-muted">Invalid WAF scope.</p>
    }
    return <WebACLDetail scope={scope} id={webAclId} name={name} />
  },
})
