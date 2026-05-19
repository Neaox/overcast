import type { FilteredLogEvent, SearchedLogStream } from "@aws-sdk/client-cloudwatch-logs"

export type {
  LogGroup,
  LogStream,
  OutputLogEvent as LogEvent,
  FilteredLogEvent,
  SearchedLogStream,
} from "@aws-sdk/client-cloudwatch-logs"

export interface FilterLogEventsResult {
  events: FilteredLogEvent[]
  searchedLogStreams: SearchedLogStream[]
}
