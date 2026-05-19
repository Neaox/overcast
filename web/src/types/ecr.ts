export interface EcrRepository {
  name: string
  arn: string
  uri: string
  registryId: string
  createdAt?: number
  imageTagMutability?: string
}

export interface EcrImageDetail {
  digest: string
  tags: string[]
  mediaType?: string
}

export interface EcrAuthorizationToken {
  proxyEndpoint: string
  username: string
  password: string
  expiresAt?: number
}

export interface EcrRepositoryDetail extends EcrRepository {
  images: EcrImageDetail[]
  login?: EcrAuthorizationToken
}
