// Re-export SDK types used by components
export type {
  FunctionConfiguration as LambdaFunction,
  AliasConfiguration as FunctionAlias,
  EventSourceMappingConfiguration as EventSourceMapping,
  LayersListItem as LambdaLayer,
  LayerVersionsListItem as LayerVersion,
  GetLayerVersionResponse as LayerVersionDetail,
} from "@aws-sdk/client-lambda"

// PutCodePayload is a UI-only discriminated union for the code upload form
export type PutCodePayload =
  | { type: "zip"; file: Blob; sourceKMSKeyArn?: string }
  | {
      type: "s3"
      s3Bucket: string
      s3Key: string
      s3ObjectVersion?: string
      sourceKMSKeyArn?: string
    }
  | { type: "image"; imageUri: string; sourceKMSKeyArn?: string }

// ── BFF-only types (emulator endpoints, not AWS SDK) ───────────────────────

export interface LambdaRuntimeInfo {
  id: string
  name: string
  family: string
  defaultHandler: string
  imageUri?: string
  deprecated: boolean
  supported: boolean
}

export interface LambdaFunctionSource {
  source: string
  filename: string
  language: string
  files: LambdaSourceFile[] | null
  /**
   * The source is an illustrative example, not the function's own code — the
   * emulator could not read a deployment package for it. Show it as a sample
   * rather than as what will run.
   */
  placeholder?: boolean
}

export interface LambdaSourceFile {
  name: string
  size: number
}

export interface InvokeResult {
  statusCode: number
  payload: string | null
  functionError: string | null
  logResult: string | null
  executedVersion: string
  logGroupName: string | null
  logStreamName: string | null
}

export interface SavedTestEvent {
  name: string
  body: string
}

export interface LambdaInstance {
  instanceId: string
  functionName: string
  status: "starting" | "initializing" | "running" | "idle"
  startedAt: number
  lastUsed: number
  expiresAt: number
  logGroup: string
  logStream: string
  lastInvocationStatus?: "succeeded" | "failed"
  lastInvocationError?: string
  triggerEvent: string
  memoryUsedMB: number
  cpuPercent: number
  /**
   * True when this environment was pre-warmed by a provisioned concurrency
   * reservation. Provisioned environments are exempt from the 15-minute idle
   * TTL, so `expiresAt` is 0 for them.
   */
  provisioned?: boolean
}

export interface LambdaLayerVersionMetadata {
  layerName: string
  version: number
  hasExternalExtensions: boolean
  externalExtensions: string[]
}
