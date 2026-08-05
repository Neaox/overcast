import { waf, type WebACLSummary } from "@/services/api/waf"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<WebACLSummary>({
  id: "waf",
  cacheKey: (endpoint) => [endpoint.baseUrl, endpoint.region, "waf", "web-acls"],
  fetchAll: () => waf.listAllWebACLs(),
  matchFields: (acl) => [acl.name, acl.id, acl.arn, acl.description, acl.scope],
  toResult: (acl) => ({
    id: `waf:${acl.scope}:${acl.id}`,
    label: acl.name,
    sublabel: `${acl.scope} · metadata only`,
    service: "WAF",
    serviceKey: "/waf",
    type: "Web ACL",
    href: `/waf/${encodeURIComponent(acl.scope)}/${encodeURIComponent(acl.id)}/${encodeURIComponent(acl.name)}`,
  }),
})
