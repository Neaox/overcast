/**
 * AWS SDK client factory.
 *
 * Use the static make* methods to get a single pre-configured SDK client for
 * the resolved emulator endpoint. Only construct the client you actually need.
 *
 * Usage in a route handler:
 *   const s3 = AWSClientFactory.makeS3(endpoint)
 *   const { Buckets } = await s3.send(new ListBucketsCommand({}))
 */

import { S3Client } from "@aws-sdk/client-s3"
import { SQSClient } from "@aws-sdk/client-sqs"
import { SNSClient } from "@aws-sdk/client-sns"
import { DynamoDBClient } from "@aws-sdk/client-dynamodb"
import { LambdaClient } from "@aws-sdk/client-lambda"
import { type EmulatorEndpoint } from "../service-discovery.js"

// Emulator accepts any non-empty credentials without validation.
const EMULATOR_CREDENTIALS = {
  accessKeyId: "overcast",
  secretAccessKey: "overcast",
} as const

export class AWSClientFactory {
  private static baseConfig(endpoint: EmulatorEndpoint) {
    return {
      endpoint: endpoint.baseUrl,
      region: endpoint.region,
      credentials: EMULATOR_CREDENTIALS,
      tls: false,
    } as const
  }

  static makeS3(endpoint: EmulatorEndpoint): S3Client {
    return new S3Client({ ...this.baseConfig(endpoint), forcePathStyle: true })
  }

  static makeSQS(endpoint: EmulatorEndpoint): SQSClient {
    return new SQSClient(this.baseConfig(endpoint))
  }

  static makeSNS(endpoint: EmulatorEndpoint): SNSClient {
    return new SNSClient(this.baseConfig(endpoint))
  }

  static makeDynamoDB(endpoint: EmulatorEndpoint): DynamoDBClient {
    return new DynamoDBClient(this.baseConfig(endpoint))
  }

  static makeLambda(endpoint: EmulatorEndpoint): LambdaClient {
    return new LambdaClient(this.baseConfig(endpoint))
  }
}

// Re-export endpoint type so routes only need one import.
export type { EmulatorEndpoint }
