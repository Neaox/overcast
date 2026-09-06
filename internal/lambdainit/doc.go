// Package lambdainit implements Overcast's in-container Lambda init: the
// process that runs as PID 1 inside a Lambda execution environment, the way
// RAPID does on AWS.
//
// It launches the container's original ENTRYPOINT+CMD as its child, owns that
// child's stdout and stderr pipes, launches every external extension in
// /opt/extensions, proxies the Runtime API on 127.0.0.1:9001 through to the
// Overcast host, and carries the Telemetry API's record batches the other way —
// from the host down a channel it opened, out to the listener an extension
// stood up inside the sandbox, which is where AWS posts them from too.
//
// Being the parent is what makes per-invocation log attribution exact: the
// init knows which invocation was in flight when it read a line, and
// it drains both pipes before it forwards the runtime's response, so the host
// has every byte of an invocation's output before it finalises the invoke. No
// clocks, no silence heuristics. See docs/plans/lambda-in-container-init.md.
//
// The real implementation is Linux-only — it uses pipes, non-blocking reads,
// SIGCHLD reaping and wait4 — and lives in the //go:build linux files. On every
// other platform [Main] compiles to a stub that refuses to run, so a bare
// checkout still builds, vets and tests on Windows and macOS.
//
// The package depends on the standard library and
// internal/services/lambda/initproto only. The binary is copied into every
// Lambda container, so its size is a cold-start cost, and it must not depend on
// the image having a libc: distroless and FROM-scratch function images are
// supported.
package lambdainit
