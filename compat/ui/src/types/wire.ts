export type Status =
  | "pass"
  | "fail"
  | "skip"
  | "unimplemented"
  | "na"
  | "running"
  | "cancelled"
  | "queued";

export interface TestStartEvent {
  event: "test_start";
  suite: string;
  service: string;
  group: string;
  test: string;
}

export interface TestResultEvent {
  event: "test_result";
  suite: string;
  service: string;
  group: string;
  test: string;
  /** AWS API operation name for doc links. "" = no link, undefined = use test name. */
  op?: string;
  status: Status;
  duration_ms: number;
  error?: string;
}

export interface SuiteStartingEvent {
  event: "suite_starting";
  suite: string;
}

export interface SuiteErrorEvent {
  event: "suite_error";
  suite: string;
  error: string;
}

export interface RunStartEvent {
  event: "run_start";
  suite: string;
  endpoint: string;
  version?: string;
  total_tests?: number;
}

export interface RunEndEvent {
  event: "run_end";
  suite: string;
  passed: number;
  failed: number;
  skipped: number;
  unimplemented: number;
  duration_ms: number;
}

export interface RunCompleteEvent {
  event: "run_complete";
}

export interface RunResetEvent {
  event: "run_reset";
  /** Suites being re-run. Empty array = full reset. */
  suites: string[];
}

export interface BuildingEvent {
  event: "building";
  suite: string;
  message: string;
}

export interface ReadyEvent {
  event: "ready";
  suite: string;
  total_tests: number;
}

export interface BatchCompleteEvent {
  event: "batch_complete";
  suite: string;
  batch_id: string;
  passed: number;
  failed: number;
  skipped: number;
  unimplemented: number;
  duration_ms: number;
}

export interface CancelledEvent {
  event: "cancelled";
  suite: string;
  batch_id: string;
  group: string;
  test: string;
  reason?: "user" | "dependency" | "batch";
}

export type WireEvent =
  | TestResultEvent
  | TestStartEvent
  | RunStartEvent
  | RunEndEvent
  | RunCompleteEvent
  | RunResetEvent
  | SuiteStartingEvent
  | SuiteErrorEvent
  | BuildingEvent
  | ReadyEvent
  | BatchCompleteEvent
  | CancelledEvent;
