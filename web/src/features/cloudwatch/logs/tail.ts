import { StartLiveTailCommand } from "@aws-sdk/client-cloudwatch-logs"
import { awsClients } from "@/services/aws-clients"
import { endpointResolver } from "@/services/discovery"

export interface TailedLogEvent {
  timestamp: number
  ingestionTime: number
  logStreamName: string
  message: string
}

export interface TailLogEventsOptions {
  groupIdentifier: string
  streamName?: string
  streamNames?: string[]
  streamNamePrefixes?: string[]
  filterPattern?: string
  signal?: AbortSignal
}

export function parseLogFilterTerms(pattern: string): string[] {
  const terms: string[] = []
  let remaining = pattern.trim()
  while (remaining) {
    if (remaining[0] === '"') {
      const end = remaining.indexOf('"', 1)
      if (end >= 0) {
        terms.push(remaining.substring(1, end))
        remaining = remaining.substring(end + 1).trim()
      } else {
        terms.push(remaining.substring(1))
        remaining = ""
      }
    } else {
      const idx = remaining.search(/[\s\t]/)
      if (idx >= 0) {
        terms.push(remaining.substring(0, idx))
        remaining = remaining.substring(idx).trim()
      } else {
        terms.push(remaining)
        remaining = ""
      }
    }
  }
  return terms
}

function logGroupIdentifierArn(identifier: string): string {
  if (identifier.startsWith("arn:")) return identifier
  const endpoint = endpointResolver.get()
  return `arn:aws:logs:${endpoint.region}:000000000000:log-group:${identifier}`
}

export async function* tailLogEvents(opts: TailLogEventsOptions): AsyncGenerator<TailedLogEvent> {
  if (opts.signal?.aborted) return

  const logStreamNames = opts.streamNames ?? (opts.streamName ? [opts.streamName] : undefined)
  const response = await awsClients.logs().send(
    new StartLiveTailCommand({
      logGroupIdentifiers: [logGroupIdentifierArn(opts.groupIdentifier)],
      logStreamNames,
      logStreamNamePrefixes: opts.streamNamePrefixes,
      logEventFilterPattern: opts.filterPattern || undefined,
    }),
    { abortSignal: opts.signal },
  )

  try {
    for await (const frame of response.responseStream ?? []) {
      if (opts.signal?.aborted) return
      const results = frame.sessionUpdate?.sessionResults ?? []
      for (const event of results) {
        yield {
          timestamp: event.timestamp ?? 0,
          ingestionTime: event.ingestionTime ?? event.timestamp ?? 0,
          logStreamName: event.logStreamName ?? "",
          message: event.message ?? "",
        }
      }
    }
  } catch (err) {
    if (opts.signal?.aborted) return
    throw err
  }
}
