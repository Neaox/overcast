import { createFileRoute } from "@tanstack/react-router"
import { StackList } from "@/features/cloudformation/components/stack-list"

export const Route = createFileRoute("/cloudformation/")({
  head: () => ({ meta: [{ title: "CloudFormation Stacks — Overcast" }] }),
  component: StackList,
})
