// Maps compat service names to the AWS SDK v3 client URL segment.
const AWS_SDK_CLIENT: Record<string, string> = {
  s3: "s3",
  sqs: "sqs",
  dynamodb: "dynamodb",
  sns: "sns",
  lambda: "lambda",
  "cloudwatch-logs": "cloudwatch-logs",
  ses: "ses",
  iam: "iam",
  sts: "sts",
  kinesis: "kinesis",
  kms: "kms",
  ssm: "ssm",
  secretsmanager: "secrets-manager",
  eventbridge: "eventbridge",
  cloudformation: "cloudformation",
  cloudfront: "cloudfront",
  cognito: "cognito-identity-provider",
  appsync: "appsync",
  ec2: "ec2",
  ecs: "ecs",
  rds: "rds",
  stepfunctions: "sfn",
  waf: "wafv2",
  shield: "shield",
};

/** Returns the SDK v3 command docs URL, or empty string if the service is unknown. */
export function awsDocsUrl(service: string, operation: string): string {
  // API Gateway has two sub-clients: REST (v1) and HTTP (v2).
  // REST operations include "Rest" in the name (CreateRestApi, GetRestApis…);
  // HTTP API operations do not (CreateApi, GetApis…).
  if (service === "apigateway") {
    const subclient = operation.includes("Rest")
      ? "api-gateway"
      : "api-gateway-v2";
    return `https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/client/${subclient}/command/${operation}Command/`;
  }
  const client = AWS_SDK_CLIENT[service];
  if (!client) return "";
  return `https://docs.aws.amazon.com/AWSJavaScriptSDK/v3/latest/client/${client}/command/${operation}Command/`;
}
