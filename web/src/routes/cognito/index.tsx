import { createFileRoute } from "@tanstack/react-router"
import { CognitoPage } from "@/features/cognito/components/cognito-page"

export const Route = createFileRoute("/cognito/")({
  head: () => ({ meta: [{ title: "Cognito — Overcast" }] }),
  component: CognitoPage,
})
