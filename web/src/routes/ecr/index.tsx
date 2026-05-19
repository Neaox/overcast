import { createFileRoute } from "@tanstack/react-router"
import { RepositoryList } from "@/features/ecr/components/repository-list"

export const Route = createFileRoute("/ecr/")({
  head: () => ({ meta: [{ title: "ECR Repositories — Overcast" }] }),
  component: RepositoryList,
})
