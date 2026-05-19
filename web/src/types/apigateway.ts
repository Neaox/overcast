// ─── REST API v1 ───────────────────────────────────────────────────────────

export interface RestApi {
  id: string
  name: string
  description?: string
  createdDate: number
  version?: string
  endpointConfiguration?: { types?: string[] }
  tags?: Record<string, string>
  binaryMediaTypes?: string[]
  disableExecuteApiEndpoint?: boolean
  rootResourceId: string
  arn?: string
}

export interface ApiResource {
  id: string
  parentId?: string
  pathPart: string
  path: string
  resourceMethods?: Record<string, ApiMethod>
}

export interface ApiMethod {
  httpMethod: string
  authorizationType?: string
  authorizerId?: string
  apiKeyRequired?: boolean
  methodIntegration?: ApiIntegration
  methodResponses?: Record<string, ApiMethodResponse>
}

export interface ApiIntegration {
  type: string // AWS_PROXY, MOCK, HTTP, HTTP_PROXY, AWS
  integrationHttpMethod?: string
  uri?: string
  requestTemplates?: Record<string, string>
  integrationResponses?: Record<string, ApiIntegrationResponse>
}

export interface ApiMethodResponse {
  statusCode: string
  responseModels?: Record<string, string>
}

export interface ApiIntegrationResponse {
  statusCode: string
  selectionPattern?: string
  responseTemplates?: Record<string, string>
}

export interface ApiStage {
  stageName: string
  deploymentId: string
  description?: string
  createdDate?: number
}

export interface ApiDeployment {
  id: string
  description?: string
  createdDate?: number
}

// ─── HTTP API v2 ───────────────────────────────────────────────────────────

export interface HttpApi {
  apiId: string
  name: string
  protocolType: string
  description?: string
  routeSelectionExpression?: string
  corsConfiguration?: CorsConfig
  createdDate?: string
  tags?: Record<string, string>
  version?: string
  disableExecuteApiEndpoint?: boolean
  apiEndpoint?: string
  arn?: string
}

export interface CorsConfig {
  allowOrigins?: string[]
  allowMethods?: string[]
  allowHeaders?: string[]
  exposeHeaders?: string[]
  maxAge?: number
  allowCredentials?: boolean
}

export interface HttpRoute {
  routeId: string
  routeKey: string
  target?: string
  authorizationType?: string
}

export interface HttpIntegration {
  integrationId: string
  integrationType: string
  integrationUri?: string
  integrationMethod?: string
  payloadFormatVersion?: string
  description?: string
}

export interface HttpStage {
  stageName: string
  deploymentId?: string
  description?: string
  createdDate?: string
  autoDeploy?: boolean
}
