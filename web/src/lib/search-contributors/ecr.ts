import { ecr } from "@/services/api"
import type { EcrRepository } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<EcrRepository>({
  id: "ecr",
  cacheKey: (endpoint) => [
    ...(endpoint.baseUrl ? ["ecr", "repositories", endpoint.baseUrl] : ["ecr", "repositories"]),
  ],
  fetchAll: () => ecr.listRepositories(),
  matchFields: (repository) => [repository.name, repository.arn, repository.uri],
  toResult: (repository) => ({
    id: `ecr:${repository.name}`,
    label: repository.name,
    sublabel: repository.uri,
    service: "ECR",
    serviceKey: "/ecr",
    type: "Repository",
    href: `/ecr/${encodeURIComponent(repository.name)}`,
  }),
})
