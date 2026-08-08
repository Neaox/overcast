* [router] request bodies are no longer read into memory on every request. The
  `Logger` middleware buffered the whole body up front so that a `5xx` could log
  it — including a multi-megabyte S3 upload, and a second time on top of that
  when `OVERCAST_DEBUG` was on. Capture is now lazy and shared: with debug off
  only what the handler itself reads is kept, bounded at 64 KiB, and a request
  rejected before its body is read buffers nothing; with debug on the trace
  capture serves the failure log too, so the body is read once. Measured in
  [docs/dev/performance.md](docs/dev/performance.md#current-measurement-methodology).
~ [router] a trace now captures goroutine stacks for its first 20 internal hops
  and for the first 20 hops that failed, rather than for every hop. A
  CloudFormation or CDK deploy dispatches hundreds of hops through one trace,
  and a multi-KiB stack apiece cost more than it told anyone. Hops past the
  budget show "Stack trace not captured" in the console.
* [router] the per-request stack trace is captured once instead of twice. The
  `Logger` and `RequestEvents` response writers both captured one at
  `WriteHeader` time and they nest, so the second overwrote the first with a
  stack one frame further from the handler.
~ [config] `OVERCAST_DEBUG_TRACE_BUFFER` is read through the typed
  configuration rather than directly from the environment, and is now
  documented alongside the other environment variables. Name and default
  (`1000`) are unchanged.
