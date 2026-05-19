/**
 * ArnLink / ResourceLink — link a resource to its detail page.
 *
 * `ArnLink`      — takes a raw ARN; service and resource extracted automatically.
 * `ResourceLink` — takes either an ARN (auto-detected) or a service + resourceId pair.
 *                  Accepts both short service names ("sqs") and CFN resource types
 *                  ("AWS::SQS::Queue") so CloudFormation stacks and other callers
 *                  don't need a mapping layer.
 *
 * Supported services / CFN types → route:
 *   sqs          / AWS::SQS::Queue            → /sqs/$queue
 *   sns          / AWS::SNS::Topic            → /sns/$topic
 *   lambda       / AWS::Lambda::Function      → /lambda/$name
 *   lambda layer / AWS::Lambda::LayerVersion  → /lambda/layers/$layerName
 *   dynamodb     / AWS::DynamoDB::Table       → /dynamodb/$tableName
 *   s3           / AWS::S3::Bucket            → /s3/$bucket
 *   logs         / AWS::Logs::LogGroup        → /cloudwatch/logs/group?groupName=…
 *   cloudformation / AWS::CloudFormation::Stack → /cloudformation/$stackName
 *   secretsmanager / AWS::SecretsManager::Secret → /secretsmanager/$secretName
 *   kinesis      / AWS::Kinesis::Stream       → /kinesis/$streamName
 *   ssm          / AWS::SSM::Parameter        → /ssm/$name
 *   rds          / AWS::RDS::DBInstance       → /rds/$instance
 *   cognito      / AWS::Cognito::UserPool     → /cognito/$poolId
 *   appsync      / AWS::AppSync::GraphQLApi   → /appsync/$apiId
 *   eventbridge  / AWS::Events::EventBus      → /eventbridge/$busName
 *
 * Unrecognised services render as plain text — no link, no error.
 */
import { Fragment } from "react"
import type { MouseEventHandler } from "react"
import { Link } from "@tanstack/react-router"
import { cn } from "@/lib/utils"
import { endpointStore } from "@/services/endpoint-store"

// ─── ArnText ─────────────────────────────────────────────────────────────────

interface ArnTextProps {
  arn: string
  /** Extra Tailwind classes merged onto the base font-mono/text-xs. */
  className?: string
}

/**
 * Renders an ARN string as monospace text with word-break opportunities
 * inserted after every `:` and `/`. This lets the browser wrap at natural
 * ARN segment boundaries without corrupting the text content — `<wbr>` is
 * not included when the text is selected and copied.
 */
export function ArnText({ arn, className }: ArnTextProps) {
  // Split on `:` and `/`, keeping each delimiter in the token stream.
  // Fragment has no DOM presence, so the only extra elements emitted are
  // the `<wbr>` hints — one per `:` or `/`.
  const tokens = arn.split(/([:/])/)
  return (
    // break-normal resets any inherited word-break:break-all from a parent
    // container so that <wbr> hints are respected. [overflow-wrap:anywhere]
    // still allows a break mid-segment as a last resort if a segment is too
    // long to fit on one line.
    <span className={cn("font-mono text-xs break-normal [overflow-wrap:anywhere]", className)}>
      {tokens.map((token, i) =>
        token === ":" || token === "/" ? (
          <Fragment key={i}>{token}<wbr /></Fragment>
        ) : (
          <Fragment key={i}>{token}</Fragment>
        ),
      )}
    </span>
  )
}

// ─── Internal route resolution ────────────────────────────────────────────────

type ResolvedRoute =
  | { kind: "params"; to: string; params: Record<string, string> }
  | { kind: "search"; to: string; search: Record<string, string> }

/** Resolve an ARN to a UI route. Returns null for unknown/unparseable ARNs. */
function resolveArn(arn: string): ResolvedRoute | null {
  if (!arn.startsWith("arn:")) return null
  const parts = arn.split(":")
  if (parts.length < 6) return null

  const service = parts[2]

  switch (service) {
    case "sqs": {
      const queue = parts[5]
      if (queue) return { kind: "params", to: "/sqs/$queue", params: { queue } }
      break
    }
    case "sns": {
      // subscription ARNs have 7+ parts — no dedicated page
      if (parts.length === 6) {
        const topic = parts[5]
        if (topic) return { kind: "params", to: "/sns/$topic", params: { topic } }
      }
      break
    }
    case "lambda": {
      const resourceType = parts[5]
      if (resourceType === "function") {
        const name = parts[6]
        if (name) return { kind: "params", to: "/lambda/$name", params: { name } }
      } else if (resourceType === "layer") {
        const layerName = parts[6]
        if (layerName)
          return { kind: "params", to: "/lambda/layers/$layerName", params: { layerName } }
      }
      break
    }
    case "dynamodb": {
      const tableMatch = parts.at(5)?.match(/^table\/([^/]+)/)
      if (tableMatch)
        return { kind: "params", to: "/dynamodb/$tableName", params: { tableName: tableMatch[1] } }
      break
    }
    case "s3": {
      const bucket = parts[5]
      if (bucket) return { kind: "params", to: "/s3/$bucket", params: { bucket } }
      break
    }
    case "logs": {
      if (parts[5] === "log-group") {
        const groupName = parts
          .slice(6)
          .join(":")
          .replace(/:log-stream:.*$/, "")
        if (groupName)
          return { kind: "search", to: "/cloudwatch/logs/group", search: { groupName } }
      }
      break
    }
    case "cloudformation": {
      const stackMatch = parts.at(5)?.match(/^stack\/([^/]+)/)
      if (stackMatch)
        return {
          kind: "params",
          to: "/cloudformation/$stackName",
          params: { stackName: stackMatch[1] },
        }
      break
    }
    case "secretsmanager": {
      // arn:aws:secretsmanager:region:account:secret:name-suffix
      const secretName = parts[6] ?? parts[5]
      if (secretName)
        return { kind: "params", to: "/secretsmanager/$secretName", params: { secretName } }
      break
    }
    case "kinesis": {
      // arn:aws:kinesis:region:account:stream/name
      const streamMatch = parts.at(5)?.match(/^stream\/(.+)/)
      if (streamMatch)
        return {
          kind: "params",
          to: "/kinesis/$streamName",
          params: { streamName: streamMatch[1] },
        }
      break
    }
    case "ssm": {
      // arn:aws:ssm:region:account:parameter/name
      const paramMatch = parts.at(5)?.match(/^parameter\/(.+)/)
      if (paramMatch) return { kind: "params", to: "/ssm/$name", params: { name: paramMatch[1] } }
      break
    }
    case "rds": {
      const dbMatch = parts.at(5)?.match(/^db:(.+)/)
      if (dbMatch) return { kind: "params", to: "/rds/$instance", params: { instance: dbMatch[1] } }
      break
    }
    case "cognito-idp": {
      // arn:aws:cognito-idp:region:account:userpool/pool-id
      const poolMatch = parts.at(5)?.match(/^userpool\/(.+)/)
      if (poolMatch)
        return { kind: "params", to: "/cognito/$poolId", params: { poolId: poolMatch[1] } }
      break
    }
    case "appsync": {
      // arn:aws:appsync:region:account:apis/apiId
      const apiMatch = parts.at(5)?.match(/^apis\/(.+)/)
      if (apiMatch) return { kind: "params", to: "/appsync/$apiId", params: { apiId: apiMatch[1] } }
      break
    }
    case "events": {
      // arn:aws:events:region:account:event-bus/name
      const busMatch = parts.at(5)?.match(/^event-bus\/(.+)/)
      if (busMatch)
        return { kind: "params", to: "/eventbridge/$busName", params: { busName: busMatch[1] } }
      break
    }
  }
  return null
}

// Normalise CFN types ("AWS::S3::Bucket") → short service name ("s3")
const CFN_TYPE_TO_SERVICE: Record<string, string> = {
  "AWS::SQS::Queue": "sqs",
  "AWS::SNS::Topic": "sns",
  "AWS::Lambda::Function": "lambda",
  "AWS::Lambda::LayerVersion": "lambda:layer",
  "AWS::DynamoDB::Table": "dynamodb",
  "AWS::S3::Bucket": "s3",
  "AWS::Logs::LogGroup": "logs",
  "AWS::CloudWatch::LogGroup": "logs",
  "AWS::CloudFormation::Stack": "cloudformation",
  "AWS::SecretsManager::Secret": "secretsmanager",
  "AWS::Kinesis::Stream": "kinesis",
  "AWS::SSM::Parameter": "ssm",
  "AWS::RDS::DBInstance": "rds",
  "AWS::Cognito::UserPool": "cognito",
  "AWS::AppSync::GraphQLApi": "appsync",
  "AWS::Events::EventBus": "eventbridge",
}

/**
 * Resolve a plain resource ID + service name (or CFN type) to a UI route.
 * Returns null for unsupported services.
 */
function resolveService(service: string, resourceId: string): ResolvedRoute | null {
  const svc = CFN_TYPE_TO_SERVICE[service] ?? service.toLowerCase()

  switch (svc) {
    case "sqs": {
      // resourceId may be a full queue URL — extract name from the last path segment
      const queue = resourceId.split("/").pop() ?? resourceId
      return { kind: "params", to: "/sqs/$queue", params: { queue } }
    }
    case "sns": {
      // resourceId may be a full ARN — extract topic name from last colon segment
      const topic = resourceId.includes("arn:")
        ? ((resolveArn(resourceId) as { params: { topic: string } } | null)?.params.topic ??
          resourceId.split(":").pop() ??
          resourceId)
        : resourceId
      return { kind: "params", to: "/sns/$topic", params: { topic } }
    }
    case "lambda":
      return { kind: "params", to: "/lambda/$name", params: { name: resourceId } }
    case "lambda:layer":
      return { kind: "params", to: "/lambda/layers/$layerName", params: { layerName: resourceId } }
    case "dynamodb":
      return { kind: "params", to: "/dynamodb/$tableName", params: { tableName: resourceId } }
    case "s3":
      return { kind: "params", to: "/s3/$bucket", params: { bucket: resourceId } }
    case "logs":
      return { kind: "search", to: "/cloudwatch/logs/group", search: { groupName: resourceId } }
    case "cloudformation":
      return {
        kind: "params",
        to: "/cloudformation/$stackName",
        params: { stackName: resourceId },
      }
    case "secretsmanager":
      return {
        kind: "params",
        to: "/secretsmanager/$secretName",
        params: { secretName: resourceId },
      }
    case "kinesis":
      return { kind: "params", to: "/kinesis/$streamName", params: { streamName: resourceId } }
    case "ssm":
      return { kind: "params", to: "/ssm/$name", params: { name: resourceId } }
    case "rds":
      return { kind: "params", to: "/rds/$instance", params: { instance: resourceId } }
    case "cognito":
      return { kind: "params", to: "/cognito/$poolId", params: { poolId: resourceId } }
    case "appsync":
      return { kind: "params", to: "/appsync/$apiId", params: { apiId: resourceId } }
    case "eventbridge":
      return { kind: "params", to: "/eventbridge/$busName", params: { busName: resourceId } }
  }
  return null
}

// ─── Shared link renderer ─────────────────────────────────────────────────────

function RouteLink({
  route,
  children,
  className,
  onClick,
}: {
  route: ResolvedRoute
  children: React.ReactNode
  className?: string
  onClick?: MouseEventHandler
}) {
  // Always include the active region so that middle-click / open-in-new-tab
  // opens the correct region without relying on sessionStorage being copied.
  const region = endpointStore.get().region

  if (route.kind === "params")
    return (
      <Link
        from="/"
        to={route.to}
        params={route.params}
        search={{ region }}
        className={className}
        onClick={onClick}
      >
        {children}
      </Link>
    )
  return (
    <Link
      to={route.to}
      search={{ ...route.search, region }}
      className={className}
      onClick={onClick}
    >
      {children}
    </Link>
  )
}

// ─── ArnLink ─────────────────────────────────────────────────────────────────

interface ArnLinkProps {
  arn: string
  /** Display text. Defaults to the ARN itself. */
  label?: string
  /** Extra Tailwind classes merged onto the base font-mono/text-xs. */
  className?: string
}

/**
 * Renders an ARN as monospace text. Links to its detail page when the service
 * is recognised; plain text otherwise.
 */
export function ArnLink({ arn, label, className }: ArnLinkProps) {
  const base = cn("font-mono text-xs", className)
  const route = resolveArn(arn)
  const content = label != null ? <span>{label}</span> : <ArnText arn={arn} />
  if (!route) return <span className={base}>{content}</span>
  return (
    <RouteLink route={route} className={cn(base, "text-accent hover:underline")}>
      {content}
    </RouteLink>
  )
}

// ─── ResourceLink ─────────────────────────────────────────────────────────────

interface ResourceLinkProps {
  /**
   * Raw ARN — service and resource extracted automatically.
   * When provided, `service` and `resourceId` are ignored.
   */
  arn?: string
  /**
   * Short service name ("sqs") or AWS CloudFormation resource type
   * ("AWS::SQS::Queue"). Required when `arn` is not provided.
   */
  service?: string
  /**
   * Physical / logical resource identifier (queue name, table name, etc.).
   * Required when `arn` is not provided.
   */
  resourceId?: string
  /** Display text. Defaults to `arn ?? resourceId`. */
  label?: string
  className?: string
  onClick?: MouseEventHandler
}

/**
 * Links a resource to its detail page using either an ARN or a
 * service + resourceId pair. Renders plain text when the service is
 * unrecognised or neither arn nor resourceId is provided.
 */
export function ResourceLink({
  arn,
  service,
  resourceId,
  label,
  className,
  onClick,
}: ResourceLinkProps) {
  const display = label ?? arn ?? resourceId ?? ""
  const linked = cn("text-accent hover:underline", className)

  // Prefer ARN resolution; fall back to service+id resolution
  const route =
    arn != null
      ? resolveArn(arn)
      : service != null && resourceId != null
        ? resolveService(service, resourceId)
        : null

  if (!route) return <span className={className}>{display}</span>
  return (
    <RouteLink route={route} className={linked} onClick={onClick}>
      {display}
    </RouteLink>
  )
}
