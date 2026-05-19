import { createFileRoute } from "@tanstack/react-router"
import { SsmPage } from "@/features/ssm/components/ssm-page"

export const Route = createFileRoute("/ssm/")({
  head: () => ({ meta: [{ title: "SSM Parameter Store — Overcast" }] }),
  component: SsmPage,
})
