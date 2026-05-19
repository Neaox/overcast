import { APIGatewayClient } from "@aws-sdk/client-api-gateway";
import { CloudFormationClient } from "@aws-sdk/client-cloudformation";
import { CloudWatchLogsClient } from "@aws-sdk/client-cloudwatch-logs";
import { DynamoDBClient } from "@aws-sdk/client-dynamodb";
import { EC2Client } from "@aws-sdk/client-ec2";
import { EventBridgeClient } from "@aws-sdk/client-eventbridge";
import { IAMClient } from "@aws-sdk/client-iam";
import { KMSClient } from "@aws-sdk/client-kms";
import { LambdaClient } from "@aws-sdk/client-lambda";
import { S3Client } from "@aws-sdk/client-s3";
import { SecretsManagerClient } from "@aws-sdk/client-secrets-manager";
import { SFNClient } from "@aws-sdk/client-sfn";
import { SNSClient } from "@aws-sdk/client-sns";
import { SQSClient } from "@aws-sdk/client-sqs";
import { SSMClient } from "@aws-sdk/client-ssm";
import { NodeHttpHandler } from "@smithy/node-http-handler";

const CREDENTIALS = {
  accessKeyId: "overcast",
  secretAccessKey: "overcast",
} as const;

function baseConfig(endpoint: string, region: string) {
  return {
    endpoint,
    region,
    credentials: CREDENTIALS,
    requestHandler: new NodeHttpHandler(),
    tls: false,
  } as const;
}

export interface Clients {
  apigateway: APIGatewayClient;
  cloudformation: CloudFormationClient;
  cloudwatchLogs: CloudWatchLogsClient;
  dynamodb: DynamoDBClient;
  ec2: EC2Client;
  eventbridge: EventBridgeClient;
  iam: IAMClient;
  kms: KMSClient;
  lambda: LambdaClient;
  s3: S3Client;
  secretsmanager: SecretsManagerClient;
  sfn: SFNClient;
  sns: SNSClient;
  sqs: SQSClient;
  ssm: SSMClient;
}

export function makeClients(endpoint: string, region: string): Clients {
  const cfg = baseConfig(endpoint, region);
  return {
    apigateway: new APIGatewayClient(cfg),
    cloudformation: new CloudFormationClient(cfg),
    cloudwatchLogs: new CloudWatchLogsClient(cfg),
    dynamodb: new DynamoDBClient(cfg),
    ec2: new EC2Client(cfg),
    eventbridge: new EventBridgeClient(cfg),
    iam: new IAMClient(cfg),
    kms: new KMSClient(cfg),
    lambda: new LambdaClient(cfg),
    s3: new S3Client({ ...cfg, forcePathStyle: true }),
    secretsmanager: new SecretsManagerClient(cfg),
    sfn: new SFNClient(cfg),
    sns: new SNSClient(cfg),
    sqs: new SQSClient(cfg),
    ssm: new SSMClient(cfg),
  };
}
