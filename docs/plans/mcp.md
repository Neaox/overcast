# Overcast MCP Servers

Status: current reference for the two MCP servers shipped by Overcast.

This document explains what the two MCP servers are, why both exist, how they
relate to each other, what capabilities they expose today, and which gaps are
still intentionally left for later.

The two servers are:

- the workspace MCP server, started from `cmd/overcast-mcp`
- the runtime MCP server, exposed by a running Overcast instance at `/_mcp`

They share one protocol core in `internal/mcp`, but they are different
deployment units with different responsibilities.

## At A Glance

### Workspace MCP

Purpose:

- Give agents and developers cheap, local, repo-aware information without
  needing a running Overcast instance.
- Help an MCP client understand the codebase, docs, conventions, coverage,
  related files, build/test commands, and impact of local changes.
- Help discover and probe running Overcast instances when live runtime truth is
  needed.

Launch model:

- Usually started by the editor over stdio.
- Can also be run over HTTP for debugging, but stdio is the intended default.

Source of truth:

- Repository structure and local workspace facts.
- Runtime discovery and delegation metadata, but not live emulator state
  ownership.

### Runtime MCP

Purpose:

- Give agents and developers direct access to the live state of a running
  Overcast instance.
- Expose runtime health, config, service state, events, typed resources, and a
  bounded set of safe mutating debug actions.

Launch model:

- Served by the running Overcast process itself.
- Mounted at `/_mcp` in non-slim builds only.

Source of truth:

- Live runtime state for that specific Overcast instance.
- Runtime-side resources and debug operations.

## Why There Are Two MCP Servers

Overcast uses two MCP servers because repository questions and live-instance
questions are different problems.

The workspace MCP is optimized for cheap local reasoning:

- What files implement a service?
- What tests should run for these changed paths?
- What does the project convention say about this handler?
- Which Overcast instances appear to be running nearby?

The runtime MCP is optimized for live-instance truth:

- What services are enabled in this running process?
- What resources exist right now?
- What recent events has this instance emitted?
- Can I safely create a bucket, purge an SQS queue, or inspect bounded state?

Keeping them separate avoids two bad outcomes:

- making the workspace server heavyweight by embedding runtime handlers
- making a sidecar pretend it is authoritative about live state it does not own

Rule of thumb:

- Use the workspace MCP for repo understanding, impact analysis, and runtime
  discovery.
- Use the runtime MCP for live state, resources, and bounded runtime mutation.

## Architecture And Boundary

Shared core:

- `internal/mcp/server.go` provides the shared MCP server implementation.
- `internal/mcp/protocol_core.go` provides shared protocol helpers and
  capability declaration.
- Both servers use the same lifecycle rules, error model, capability honesty,
  pagination helpers, structured tool result helpers, and logging/resource/
  prompt/completion plumbing.

Separate ownership:

- `cmd/overcast-mcp` builds the standalone workspace server.
- `internal/router/mcp_routes.go` mounts the runtime server into Overcast.
- The workspace binary must not ship runtime `/_mcp` HTTP handlers.
- The runtime server must remain attached to the running Overcast instance and
  its state store.

Operational boundary:

- The workspace MCP can discover and call runtime MCP endpoints.
- It should not reimplement runtime state logic locally just to avoid an extra
  hop.
- When runtime truth matters, the runtime MCP is the authority.

## Transport And Exposure Model

### Workspace MCP transport

- Primary transport: stdio
- Secondary transport: local HTTP for debugging or non-editor clients
- Typical entrypoint: `go build -o ./bin/overcast-mcp ./cmd/overcast-mcp`
- Typical editor startup: `.vscode/mcp.json` launches it automatically over
  stdio

### Runtime MCP transport

- Primary transport: Streamable HTTP at `/_mcp`
- Supported methods:
  - `GET /_mcp` for SSE streams
  - `POST /_mcp` for JSON-RPC request/response and SSE response mode
  - `DELETE /_mcp` for session termination
  - `GET /_mcp/sse` as a legacy compatibility endpoint
- Available only in non-slim builds

### Local-only posture

Both MCP servers are intended for local development and debugging.

- The workspace MCP is a local tool server.
- The runtime MCP is a local Overcast debugging surface.
- Origin validation still matters for the runtime HTTP surface because local
  browser-based or tool-based callers should not be able to cross-origin into
  it accidentally.
- Remote-hardening concerns such as full OAuth-based authorization are not a
  current product requirement because these servers are not intended as remote
  production-facing services.

## Protocol And Capability Contract

Both servers target MCP `2025-11-25` for the capabilities they advertise.

Shared protocol behavior includes:

- initialize / initialized lifecycle gating
- `ping`
- request vs notification handling
- capability advertisement through one shared declaration path
- JSON-RPC error normalization
- pagination helpers for list methods
- structured tool results
- resources, prompts, completions, and logging support
- resource subscriptions and list-changed notifications
- prompt and tool list-changed notifications
- logging level negotiation and `notifications/message`

Important rule:

- Overcast should only advertise capabilities it actually implements.
- Unsupported optional methods should fail clearly with the correct MCP/
  JSON-RPC behavior, not by silent acceptance.

### Capabilities currently advertised

- `tools`
- `resources`
- `prompts`
- `completions`
- `logging`

### Capabilities intentionally not advertised today

- `tasks`
- `roots`
- `sampling`
- `elicitation`

These are intentionally out of scope for now.

## Workspace MCP: What It Does Today

The workspace MCP is the repo-aware assistant for this repository.

Broad capability groups:

- workspace identity and orientation
- repository navigation and file lookup
- service coverage and operation support metadata
- documentation and companion-file discovery
- symbol and topology discovery
- change-impact and focused test targeting
- bounded compatibility reruns
- runtime discovery and delegation to running Overcast instances

Representative workspace tools:

- `workspace_server_info`
- `repo_workspace_info`
- `repo_build_commands`
- `repo_find_todos`
- `repo_service_coverage`
- `repo_service_files`
- `repo_doc_coverage`
- `repo_cloudformation_links`
- `repo_service_manifest`
- `repo_operation_support`
- `repo_find_symbol`
- `repo_conventions_snapshot`
- `repo_topology_contributors`
- `repo_related_files`
- `repo_endpoint_map`
- `repo_change_impact`
- `repo_test_targets`
- `repo_compat_rerun_subset`
- `repo_list_files`
- `repo_read_file_snippet`
- `repo_service_capabilities`

Runtime-discovery and delegation tools exposed by the workspace MCP:

- `runtime_list_instances`
- `runtime_probe_instance`
- `runtime_refresh_probe_cache`
- `runtime_mcp_call`
- `runtime_list_services`
- `runtime_get_health`
- `runtime_get_config`
- `runtime_get_service_state`
- `runtime_get_recent_events`
- `runtime_probe_kv_store`

What the workspace MCP is good at:

- helping an agent get context quickly without large codebase scans
- answering repository questions without starting Overcast
- identifying impacted services and validation commands
- helping an agent find a live Overcast instance and negotiate with it

What it should not do:

- pretend to own live runtime state
- expose raw, unbounded internal state dumps
- embed Overcast runtime handlers into the workspace binary

## Runtime MCP: What It Does Today

The runtime MCP is the live-instance assistant for a running Overcast process.

Broad capability groups:

- instance identity and service inventory
- runtime health and config inspection
- bounded service-state and event inspection
- typed resources and resource templates
- safe mutating debug actions against live runtime state

Representative runtime tools:

- `runtime_instance_info`
- `runtime_list_services`
- `runtime_inventory`
- `runtime_get_health`
- `runtime_get_config`
- `runtime_get_service_state`
- `runtime_get_recent_events`
- `runtime_state_scan`
- `runtime_probe_kv_store`

Current bounded mutation coverage includes runtime actions for services such as:

- S3
- SQS
- DynamoDB
- SNS
- Kinesis
- KMS
- Step Functions
- SSM
- Secrets Manager
- IAM
- ACM

Examples of runtime mutations exposed through MCP:

- create or delete an S3 bucket
- create, update, delete, or purge an SQS queue
- create a DynamoDB table or update TTL
- create or update SNS topics
- inspect or mutate supported IAM and ACM entities

The runtime provider also exposes typed resources for supported services and
emits MCP resource notifications when runtime mutations affect those resources.

What the runtime MCP is good at:

- debugging a live Overcast process directly
- inspecting current service or resource state with bounded outputs
- driving small safe runtime mutations during development
- acting as the source of truth for the state of a specific instance

What it should not do:

- become a general remote administration surface
- expose unbounded secrets or raw store dumps
- replace the AWS-compatible API surface

## Resources, Prompts, Completions, And Logging

These capabilities are shared protocol features, but the useful content comes
from the active provider.

### Resources

- `resources/list`, `resources/read`, and `resources/templates/list` are
  implemented.
- The runtime provider contributes the richest resources because it has live
  instance state.
- Resource subscriptions are supported, and runtime mutations emit
  `notifications/resources/updated` and `notifications/resources/list_changed`
  where appropriate.

### Prompts

- `prompts/list` and `prompts/get` are implemented.
- Prompts are intentionally lightweight and currently serve as helper prompts,
  not a large product surface.

### Completions

- `completion/complete` is implemented.
- Current completion quality is strongest for prompt names/titles and resource
  or template prefixes.

### Logging

- `logging/setLevel` is implemented.
- `notifications/message` is emitted using RFC 5424-style level names.
- Server-emitted log notifications include the `logger` field so clients can
  attribute the source to Overcast.

## How The Two Servers Work Together

Typical flow:

1. An editor or agent starts the workspace MCP over stdio.
2. The workspace MCP answers repo-local questions immediately.
3. If live-instance information is needed, the workspace MCP discovers nearby
   Overcast instances.
4. The workspace MCP either probes or directly calls the runtime MCP.
5. When deep live-state work is required, the runtime MCP becomes the direct
   source of truth.

In practice, this means:

- repo questions stay cheap and local
- runtime truth stays attached to the process that owns it
- agents can move from code understanding to live debugging without changing
  mental models or protocol semantics

## What Is Intentionally Out Of Scope Right Now

The following are not current goals:

- turning MCP into a general shell or arbitrary command execution surface
- advertising `tasks` before the MCP task model is more settled and useful
- advertising `roots`, `sampling`, or `elicitation` without a concrete
  Overcast workflow that needs them
- making runtime MCP a remote production-facing management API

These are deferred by design, not forgotten accidentally.

## Known Gaps Worth Filling Later

The current implementation is strong enough to be useful and honest, but there
are still some areas that could be improved later.

### Likely future improvements

- richer argument-aware completions beyond current prompt/resource/template
  prefix help
- broader and deeper typed runtime resource coverage as more services gain
  better MCP representations
- more prompt content only if it proves genuinely useful for agent workflows
- configurable or persistent notification replay if reconnect durability becomes
  important during long debugging sessions
- additional safe mutating runtime actions where the safety boundary is clear

### Explicitly deferred, not active gaps

- `tasks`
- `roots`
- `sampling`
- `elicitation`

These should stay unadvertised until Overcast has a concrete need and a clear
design for them.

## Practical Guidance For Humans And Agents

If you need repository understanding, use the workspace MCP first.

Examples:

- finding service files
- identifying impacted tests
- checking conventions
- locating topology contributors
- discovering whether an Overcast instance is available

If you need live state, use the runtime MCP.

Examples:

- reading current health or config
- listing resources that exist right now
- inspecting bounded service state or recent events
- performing a safe live mutation such as purging a queue

If both are available, the recommended order is:

1. start with the workspace MCP for orientation
2. switch to the runtime MCP when the question becomes instance-specific

## Files And Entrypoints

Workspace server:

- `cmd/overcast-mcp/main.go`
- `internal/mcp/repo_provider.go`

Runtime server:

- `internal/router/mcp_routes.go`
- `internal/mcp/runtime_provider.go`

Shared core:

- `internal/mcp/server.go`
- `internal/mcp/protocol_core.go`

Editor automation:

- `.vscode/mcp.json`

## Bottom Line

Overcast has two MCP servers on purpose.

- The workspace MCP is for local repo intelligence and runtime discovery.
- The runtime MCP is for live-instance truth and bounded debugging actions.
- They share one protocol core so clients see consistent MCP behavior across
  both surfaces.
- The boundary between them is important: workspace MCP explains the repo,
  runtime MCP explains the running process.

