const serviceColors: Record<string, string> = {
  cloudformation: "#f59e0b", lambda: "#f97316", sqs: "#8b5cf6", sns: "#ec4899",
  s3: "#22c55e", dynamodb: "#3b82f6", iam: "#ef4444", ecs: "#06b6d4", ec2: "#f97316",
  kms: "#a855f7", ssm: "#6366f1", logs: "#14b8a6", stepfunctions: "#84cc16",
  events: "#e11d48", eventbridge: "#e11d48", secretsmanager: "#d946ef", apigateway: "#0ea5e9",
  appsync: "#f43f5e", cognito: "#8b5cf6", waf: "#e11d48", cloudfront: "#06b6d4",
  pipes: "#f59e0b", kinesis: "#3b82f6", elasticache: "#eab308", rds: "#3b82f6",
  elbv2: "#06b6d4", autoscaling: "#f97316", route53: "#8b5cf6", acm: "#22c55e",
  sts: "#ef4444", transfer: "#a855f7", shield: "#e11d48", backup: "#14b8a6",
  firehose: "#f59e0b", athena: "#3b82f6", glue: "#84cc16", msk: "#ec4899",
  eks: "#f97316", efs: "#06b6d4", opensearch: "#8b5cf6", appconfig: "#22c55e",
  bedrock: "#f43f5e", scheduler: "#a855f7", cloudtrail: "#f59e0b",
  organizations: "#ef4444", ses: "#6366f1",
}

export function serviceColor(service: string): string {
  return serviceColors[service] ?? "var(--color-accent)"
}
