import {
  BatchGetImageCommand,
  CreateRepositoryCommand,
  DeleteRepositoryCommand,
  DescribeImagesCommand,
  DescribeRepositoriesCommand,
  GetAuthorizationTokenCommand,
} from "@aws-sdk/client-ecr"
import { awsClients } from "../aws-clients"
import type {
  EcrAuthorizationToken,
  EcrRepository,
  EcrRepositoryDetail,
  EcrImageDetail,
} from "@/types"

function decodeToken(token?: string): EcrAuthorizationToken | undefined {
  if (!token) return undefined
  try {
    const decoded = atob(token)
    const [username, password] = decoded.split(":", 2)
    if (!username || !password) return undefined
    return { username, password, proxyEndpoint: "" }
  } catch {
    return undefined
  }
}

function mapRepository(repo: {
  repositoryName?: string
  repositoryArn?: string
  repositoryUri?: string
  registryId?: string
  createdAt?: Date
  imageTagMutability?: string
}): EcrRepository {
  return {
    name: repo.repositoryName ?? "",
    arn: repo.repositoryArn ?? "",
    uri: repo.repositoryUri ?? "",
    registryId: repo.registryId ?? "",
    createdAt: repo.createdAt?.getTime(),
    imageTagMutability: repo.imageTagMutability,
  }
}

function mapImage(detail: {
  imageDigest?: string
  imageTags?: string[]
  imageManifestMediaType?: string
}): EcrImageDetail {
  return {
    digest: detail.imageDigest ?? "",
    tags: detail.imageTags ?? [],
    mediaType: detail.imageManifestMediaType,
  }
}

export const ecr = {
  listRepositories: async (): Promise<EcrRepository[]> => {
    const res = await awsClients.ecr().send(new DescribeRepositoriesCommand({}))
    return (res.repositories ?? []).map(mapRepository)
  },

  getRepository: async (name: string): Promise<EcrRepositoryDetail> => {
    const client = awsClients.ecr()
    const [reposRes, imagesRes, authRes] = await Promise.all([
      client.send(new DescribeRepositoriesCommand({ repositoryNames: [name] })),
      client
        .send(new DescribeImagesCommand({ repositoryName: name }))
        .catch(() => ({ imageDetails: [] })),
      client.send(new GetAuthorizationTokenCommand({})).catch(() => ({ authorizationData: [] })),
    ])
    const repo = reposRes.repositories?.[0]
    if (!repo) throw new Error(`Repository ${name} not found`)
    const authEntry = authRes.authorizationData?.[0]
    const decoded = decodeToken(authEntry?.authorizationToken)
    return {
      ...mapRepository(repo),
      images: (imagesRes.imageDetails ?? []).map(mapImage),
      login:
        authEntry?.proxyEndpoint && decoded
          ? {
              ...decoded,
              proxyEndpoint: authEntry.proxyEndpoint,
              expiresAt: authEntry.expiresAt?.getTime(),
            }
          : undefined,
    }
  },

  createRepository: async (name: string) => {
    await awsClients.ecr().send(new CreateRepositoryCommand({ repositoryName: name }))
  },

  deleteRepository: async (name: string) => {
    await awsClients.ecr().send(new DeleteRepositoryCommand({ repositoryName: name, force: true }))
  },

  batchGetImage: async (repositoryName: string, imageTag: string) => {
    const res = await awsClients.ecr().send(
      new BatchGetImageCommand({
        repositoryName,
        imageIds: [{ imageTag }],
      }),
    )
    return res.images?.[0]
  },
}
