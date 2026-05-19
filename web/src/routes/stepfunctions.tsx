import { createFileRoute } from "@tanstack/react-router"
import { StepFunctionsPage } from "@/features/stepfunctions/components/stepfunctions-page"

export const Route = createFileRoute("/stepfunctions")({
  head: () => ({ meta: [{ title: "Step Functions — Overcast" }] }),
  component: StepFunctionsPage,
})
