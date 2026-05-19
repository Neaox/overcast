import { createFileRoute } from "@tanstack/react-router"
import { FunctionList } from "@/features/lambda/components/function-list"

export const Route = createFileRoute("/lambda/")({
  head: () => ({ meta: [{ title: "Lambda Functions — Overcast" }] }),
  component: FunctionList,
})
