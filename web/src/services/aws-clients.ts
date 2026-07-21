/**
 * Browser-side AWS SDK client factory.
 *
 * Creates pre-configured SDK v3 clients that talk directly to the Overcast
 * emulator (bypassing the BFF). Reads the current endpoint from the
 * endpointResolver singleton so clients always point at the right host.
 *
 * Usage:
 *   import { awsClients } from "@/services/aws-clients"
 *   const s3 = awsClients.s3()
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
import { CloudWatchClient } from "@aws-sdk/client-cloudwatch"
import { SESv2Client } from "@aws-sdk/client-sesv2"
import { SecretsManagerClient } from "@aws-sdk/client-secrets-manager"
import { CloudFormationClient } from "@aws-sdk/client-cloudformation"
import { ECSClient } from "@aws-sdk/client-ecs"
import { EC2Client } from "@aws-sdk/client-ec2"
import { RDSClient } from "@aws-sdk/client-rds"
import { ElastiCacheClient } from "@aws-sdk/client-elasticache"
import { KafkaClient } from "@aws-sdk/client-kafka"
import { IAMClient } from "@aws-sdk/client-iam"
import { SFNClient } from "@aws-sdk/client-sfn"
import { AppSyncClient } from "@aws-sdk/client-appsync"
import { EventBridgeClient } from "@aws-sdk/client-eventbridge"
import { CloudFrontClient } from "@aws-sdk/client-cloudfront"
import { APIGatewayClient } from "@aws-sdk/client-api-gateway"
import { ApiGatewayV2Client } from "@aws-sdk/client-apigatewayv2"
import { CognitoIdentityProviderClient } from "@aws-sdk/client-cognito-identity-provider"
import { KMSClient } from "@aws-sdk/client-kms"
import { SSMClient } from "@aws-sdk/client-ssm"
import { STSClient } from "@aws-sdk/client-sts"
import { ECRClient } from "@aws-sdk/client-ecr"
import { EKSClient } from "@aws-sdk/client-eks"
import { endpointResolver } from "./discovery"

// Emulator accepts any non-empty credentials without validation.
const EMULATOR_CREDENTIALS = {
  accessKeyId: "overcast",
  secretAccessKey: "overcast",
} as const

function baseConfig() {
  const ep = endpointResolver.get()
  return {
    endpoint: ep.baseUrl,
    region: ep.region,
    credentials: EMULATOR_CREDENTIALS,
    tls: false,
    disableHostPrefix: true,
  } as const
}

/**
 * Browser SDK client factory. Each call returns a fresh client pointed at
 * the current endpoint — cheap to create, no connection pooling in browsers.
 */
export const awsClients = {
  s3: () => new S3Client({ ...baseConfig(), forcePathStyle: true }),
  sqs: () => new SQSClient(baseConfig()),
  sns: () => new SNSClient(baseConfig()),
  dynamodb: () => new DynamoDBClient(baseConfig()),
  kinesis: () => new KinesisClient(baseConfig()),
  lambda: () => new LambdaClient(baseConfig()),
  pipes: () => new PipesClient(baseConfig()),
  cloudwatch: () => new CloudWatchClient(baseConfig()),
  logs: () => new CloudWatchLogsClient(baseConfig()),
  sesv2: () => new SESv2Client(baseConfig()),
  secretsmanager: () => new SecretsManagerClient(baseConfig()),
  cloudformation: () => new CloudFormationClient(baseConfig()),
  ecs: () => new ECSClient(baseConfig()),
  ec2: () => new EC2Client(baseConfig()),
  rds: () => new RDSClient(baseConfig()),
  elasticache: () => new ElastiCacheClient(baseConfig()),
  kafka: () => new KafkaClient(baseConfig()),
  iam: () => new IAMClient(baseConfig()),
  sfn: () => new SFNClient(baseConfig()),
  appsync: () => new AppSyncClient(baseConfig()),
  eventbridge: () => new EventBridgeClient(baseConfig()),
  apigateway: () => new APIGatewayClient(baseConfig()),
  apigatewayv2: () => new ApiGatewayV2Client(baseConfig()),
  cloudfront: () => new CloudFrontClient(baseConfig()),
  cognito: () => new CognitoIdentityProviderClient(baseConfig()),
  kms: () => new KMSClient(baseConfig()),
  ssm: () => new SSMClient(baseConfig()),
  sts: () => new STSClient(baseConfig()),
  ecr: () => new ECRClient(baseConfig()),
  eks: () => new EKSClient(baseConfig()),
}
