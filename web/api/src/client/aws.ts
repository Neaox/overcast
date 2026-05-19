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
import { KinesisClient } from "@aws-sdk/client-kinesis"
import { LambdaClient } from "@aws-sdk/client-lambda"
import { PipesClient } from "@aws-sdk/client-pipes"
import { CloudWatchLogsClient } from "@aws-sdk/client-cloudwatch-logs"
import { SESv2Client } from "@aws-sdk/client-sesv2"
import { SecretsManagerClient } from "@aws-sdk/client-secrets-manager"
import { APIGatewayClient } from "@aws-sdk/client-api-gateway"
import { ApiGatewayV2Client } from "@aws-sdk/client-apigatewayv2"
import { NodeHttpHandler } from "@smithy/node-http-handler"
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

  static makeKinesis(endpoint: EmulatorEndpoint): KinesisClient {
    // Kinesis SDK defaults to HTTP/2 in Node.js; the emulator only supports
    // HTTP/1.1, so we explicitly use NodeHttpHandler to force HTTP/1.1.
    return new KinesisClient({
      ...this.baseConfig(endpoint),
      requestHandler: new NodeHttpHandler(),
    })
  }

  static makeLambda(endpoint: EmulatorEndpoint): LambdaClient {
    return new LambdaClient(this.baseConfig(endpoint))
  }

  static makePipes(endpoint: EmulatorEndpoint): PipesClient {
    return new PipesClient(this.baseConfig(endpoint))
  }

  static makeCloudWatchLogs(endpoint: EmulatorEndpoint): CloudWatchLogsClient {
    return new CloudWatchLogsClient(this.baseConfig(endpoint))
  }

  static makeSESv2(endpoint: EmulatorEndpoint): SESv2Client {
    return new SESv2Client(this.baseConfig(endpoint))
  }

  static makeSecretsManager(endpoint: EmulatorEndpoint): SecretsManagerClient {
    return new SecretsManagerClient(this.baseConfig(endpoint))
  }

  static makeAPIGateway(endpoint: EmulatorEndpoint): APIGatewayClient {
    return new APIGatewayClient(this.baseConfig(endpoint))
  }

  static makeApiGatewayV2(endpoint: EmulatorEndpoint): ApiGatewayV2Client {
    return new ApiGatewayV2Client(this.baseConfig(endpoint))
  }
}

// Re-export endpoint type so routes only need one import.
export type { EmulatorEndpoint }
