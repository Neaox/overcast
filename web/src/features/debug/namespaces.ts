export const DEBUG_SERVICE_NAMESPACES: Record<string, string[]> = {
  s3: ["s3:buckets", "s3:objects", "s3:meta"],
  sqs: ["sqs:queues", "sqs:messages"],
  sns: ["sns:topics", "sns:subscriptions"],
  dynamodb: ["dynamodb:tables", "dynamodb:items"],
  lambda: ["lambda:functions"],
}

export const DEBUG_SERVICE_LABELS: Record<string, string> = {
  s3: "S3",
  sqs: "SQS",
  sns: "SNS",
  dynamodb: "DynamoDB",
  lambda: "Lambda",
}

export function serviceForDebugNamespace(namespace: string): string | undefined {
  return Object.entries(DEBUG_SERVICE_NAMESPACES).find(([, namespaces]) =>
    namespaces.includes(namespace),
  )?.[0]
}

export function firstDebugNamespaceForService(
  service: string,
  availableNamespaces: string[],
): string {
  const allowed = DEBUG_SERVICE_NAMESPACES[service] ?? []
  return allowed.find((namespace) => availableNamespaces.includes(namespace)) ?? ""
}

export function groupDebugNamespaces(
  namespaces: string[],
): { service: string; namespaces: string[] }[] {
  const grouped = new Map<string, string[]>()
  for (const namespace of namespaces) {
    const service = serviceForDebugNamespace(namespace) ?? "other"
    const existing = grouped.get(service) ?? []
    existing.push(namespace)
    grouped.set(service, existing)
  }
  return Array.from(grouped.entries()).map(([service, ns]) => ({ service, namespaces: ns }))
}
