import { createFileRoute } from "@tanstack/react-router"
import { WebACLList } from "@/features/waf/components/web-acl-list"

export const Route = createFileRoute("/waf/")({
  head: () => ({ meta: [{ title: "WAF Web ACLs — Overcast" }] }),
  component: WebACLList,
})
