import { createFileRoute } from "@tanstack/react-router"
import { RepositoryDetail } from "@/features/ecr/components/repository-detail"

export const Route = createFileRoute("/ecr/$repositoryName")({
  head: ({ params }) => ({
    meta: [{ title: `${params.repositoryName} — ECR — Overcast` }],
  }),
  component: function RepositoryDetailRoute() {
    const { repositoryName } = Route.useParams()
    return <RepositoryDetail repositoryName={repositoryName} />
  },
})
