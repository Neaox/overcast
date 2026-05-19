import { createFileRoute } from "@tanstack/react-router"
import { SecretsManagerPage } from "@/features/secretsmanager/secrets-manager-page"

export const Route = createFileRoute("/secretsmanager/")({
  head: () => ({ meta: [{ title: "Secrets Manager — Overcast" }] }),
  component: SecretsManagerPage,
})
