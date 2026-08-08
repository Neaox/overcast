/**
 * Lambda logging configuration — the vocabulary the create wizard and the
 * configuration tab share, and the single place a `LoggingConfig` payload is
 * built.
 *
 * Three service rules a form is easy to break, so none of them is left to a
 * disabled control having been reset:
 *
 *   - `ApplicationLogLevel` and `SystemLogLevel` may only be sent when
 *     `LogFormat` is `JSON`. Either one alongside `Text` is a 400 —
 *     "ApplicationLogLevel and SystemLogLevel can only be set for functions
 *     configured to use JSON log format." — so the builder drops them.
 *   - Both default to `INFO` when the format is JSON and neither was sent,
 *     which is what `GetFunction` reports back, so the form starts there too.
 *   - `UpdateFunctionConfiguration` with an explicitly *empty* `LoggingConfig`
 *     object still answers 501: its AWS semantics have never been captured.
 *     The builder always names at least `LogFormat`, so `{}` cannot be sent.
 *
 * `LoggingConfig` is also replaced wholesale on update — the service rebuilds
 * all four members from the request and defaults the ones the request omits, so
 * a partial payload silently resets the rest. Sending only `LogFormat` would
 * move a custom log group back to `/aws/lambda/<name>`; callers therefore round
 * -trip the whole configuration through `loggingConfigForm`.
 */
import type {
  ApplicationLogLevel,
  LoggingConfig,
  LogFormat,
  SystemLogLevel,
} from "@aws-sdk/client-lambda"

/** `Text` is the service default; `JSON` is what unlocks level filtering. */
export const LOG_FORMATS = ["Text", "JSON"] as const satisfies readonly LogFormat[]

/** Most detailed first, which is the order AWS documents them in. */
export const APPLICATION_LOG_LEVELS = [
  "TRACE",
  "DEBUG",
  "INFO",
  "WARN",
  "ERROR",
  "FATAL",
] as const satisfies readonly ApplicationLogLevel[]

/** System logs have no TRACE/ERROR/FATAL — Lambda only emits these three. */
export const SYSTEM_LOG_LEVELS = [
  "DEBUG",
  "INFO",
  "WARN",
] as const satisfies readonly SystemLogLevel[]

export const DEFAULT_APPLICATION_LOG_LEVEL: ApplicationLogLevel = "INFO"
export const DEFAULT_SYSTEM_LOG_LEVEL: SystemLogLevel = "INFO"

/** The editable shape of a function's logging configuration. */
export interface LoggingConfigForm {
  /** Empty means the default `/aws/lambda/<function name>`. */
  logGroup: string
  logFormat: LogFormat
  applicationLogLevel: ApplicationLogLevel
  systemLogLevel: SystemLogLevel
}

/**
 * Seeds the form from what `GetFunction` reported. An older function stored
 * before `LoggingConfig` existed reports no format at all, which is Text.
 */
export function loggingConfigForm(config: LoggingConfig | undefined): LoggingConfigForm {
  return {
    logGroup: config?.LogGroup ?? "",
    logFormat: config?.LogFormat === "JSON" ? "JSON" : "Text",
    applicationLogLevel: config?.ApplicationLogLevel ?? DEFAULT_APPLICATION_LOG_LEVEL,
    systemLogLevel: config?.SystemLogLevel ?? DEFAULT_SYSTEM_LOG_LEVEL,
  }
}

/**
 * Builds the `LoggingConfig` member for CreateFunction or
 * UpdateFunctionConfiguration. Never returns an empty object, and never carries
 * a log level under the Text format.
 */
export function buildLoggingConfig(form: LoggingConfigForm): LoggingConfig {
  const logGroup = form.logGroup.trim()
  const base: LoggingConfig = {
    ...(logGroup ? { LogGroup: logGroup } : {}),
    LogFormat: form.logFormat,
  }
  if (form.logFormat !== "JSON") return base
  return {
    ...base,
    ApplicationLogLevel: form.applicationLogLevel,
    SystemLogLevel: form.systemLogLevel,
  }
}
