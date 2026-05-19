---
name: aws-compatibility-review
description: AWS compatibility review for Overcast services and endpoints. Use when auditing service fidelity against AWS docs, adding compatibility tests, fixing wire-format or behavior drift, or updating docs/compatibility tracking files.
---

# AWS Compatibility Review

Use this skill when reviewing or fixing Overcast service behavior against AWS documentation. The goal is systematic AWS-fidelity coverage that can be resumed across sessions and agents.

For issue-backed tracking, also use the `github-issue-lifecycle` skill. Compatibility review progress should be tracked in GitHub Issues, with repository compatibility docs updated as the durable implementation/status record.

## Source Of Truth

Escalate in this order:

1. AWS API Reference documentation.
2. AWS Developer Guide documentation.
3. Existing Overcast implementation, tests, and compatibility docs.
4. Other emulators only as supporting evidence.
5. Real AWS only with explicit user permission.

Never create or call real AWS resources unless the user explicitly approves it in the current conversation.

## Mandatory Start

Before reviewing or changing an endpoint:

1. Use `github-issue-lifecycle` to find or create the tracking issue unless the user explicitly asks for local-only review.
2. Read the tracking issue, comments, labels, and linked PRs before editing code.
3. Read `docs/compatibility/README.md`.
4. Read `docs/compatibility/matrix.yaml`.
5. Read `docs/compatibility/services/<service>.yaml` if it exists.
6. Pick the highest-priority operation whose status is not `reviewed` unless the user named a specific operation or issue.
7. Fetch and read the linked AWS docs for the operation before editing code.

## Issue Tracking

Compatibility reviews are actionable work and should normally have a GitHub Issue.

Required issue labels:

- `compat`
- `aws-fidelity`
- `area/emulation`
- `service/<name>`
- One lifecycle label, usually `status/ready`, `status/in-progress`, `status/blocked`, or `status/needs-review`

Use additional labels when applicable:

- `needs-tests` when behavior is known but coverage is missing.
- `needs-aws-verification` only when docs and existing evidence are insufficient and real AWS may be required.
- `implementation-mismatch` when Overcast behavior diverges from documented AWS behavior.
- `intentionally-partial` when a documented AWS behavior is intentionally outside current scope.

Issue title format:

- `Compat review: <service> <operation/resource>`

Before creating a new compat issue:

1. Search open and closed issues for the service and operation.
2. Reuse an existing issue if it already covers the work.
3. If no issue exists, create one using `.github/ISSUE_TEMPLATE/compat_review.md`.
4. Apply the concrete `service/<name>` label manually. Do not use placeholder labels such as `service/<service>`.

When starting work on an issue, move it to `status/in-progress`. When handing off without completion, comment with findings, verification, blockers, and next steps. When ready for maintainer review, move it to `status/needs-review`.

## Operation Checklist

For each operation, compare AWS docs with Overcast for:

- Protocol, target header, path, query/body encoding, and content type.
- Required parameters.
- Optional parameters and defaults.
- Parameter validation and constraints.
- Success status code and response shape.
- Empty-result behavior.
- Error codes, messages where stable, and HTTP status codes.
- Pagination tokens, limits, and ordering.
- Idempotency and duplicate handling.
- State transitions.
- ARN, account ID, region, URL, and identifier formats.
- Timestamp formats and units.
- SDK compatibility.
- Performance - be aware of performance degredation - keep the hot path as a clean as possible - ensure there are no memory leaks or go-routine leaks and that cancellation contexts are handled correctly.
- Storage layer - ensure that data is stored and retreived correctly (across storage models - memory, hybrid, wal etc)

## Scenario Matrix

Do not stop at the default resource shape. AWS docs often call out behavior that only appears under specific resource configuration. For each operation, build a scenario matrix from the docs before writing tests.

Include applicable scenarios for:

- Default/minimal resource configuration.
- Common production-style configuration.
- Mutually exclusive configuration modes.
- Optional feature flags enabled and disabled.
- Resource state variations, including newly-created, active, disabled, deleting, expired, confirmed/unconfirmed, empty/non-empty, or in-flight states.
- Identifier variants, including name, ARN, URL, generated ID, alias, and case-sensitive/case-insensitive forms.
- Duplicate, already-exists, missing, stale, and deleted resources.
- Cross-resource relationships, including subscriptions, event source mappings, queues with DLQs, Cognito clients in user pools, and CloudFormation-created resources.
- Boundary values, including minimum, maximum, zero, omitted, malformed, and unsupported enum values.
- Protocol variants when a service supports more than one protocol.

Document every scenario as one of:

- Tested and matching AWS docs.
- Intentionally partial, with a documented gap.
- Unsupported.
- Blocked because docs are ambiguous.

Example for Cognito username behavior:

- Plain username pool: `Username` is the stored username.
- `UsernameAttributes: [email]`: `SignUp`/`AdminCreateUser` use generated UUID username and email is accepted as `Username` in most APIs.
- `UsernameAttributes: [phone_number]`: same behavior for phone number.
- `AliasAttributes: [email]`: user has a fixed username and verified email can be an alias; duplicate/verified alias and `ForceAliasCreation` behavior must be reviewed separately.
- `ListUsers`: email/phone are filters; username filter uses the stored UUID for username-attribute pools.

## Testing Rules

- Add or update tests before implementation changes.
- Tests must exercise AWS-doc-confirmed behavior.
- Tests must cover the scenario matrix for any behavior Overcast claims to support, not only the happy path.
- Use integration tests for HTTP/wire behavior.
- Use unit tests for narrow helper, store, and cancellation edge cases.
- Prefer AWS SDK clients for management-plane compatibility when practical.
- Do not mark an operation `reviewed` unless tests cover the documented behavior that Overcast claims to support.

## Status Values

Use only these status values in compatibility trackers:

- `not_started`
- `docs_reviewed`
- `tests_missing`
- `tests_added`
- `implementation_mismatch`
- `fixed`
- `reviewed`
- `blocked`
- `intentionally_partial`
- `unsupported`

## Tracker Updates

Before finishing, update:

- The GitHub issue with progress, evidence, verification commands, remaining gaps, and next-agent handoff.
- `docs/compatibility/services/<service>.yaml` with operation status, AWS docs URLs, scenario matrix, tests, evidence, gaps, findings, and next-agent handoff.
- `docs/compatibility/matrix.yaml` with service-level status, priority, claim fields if used, progress counts, focus areas, and next action.
- `CHANGELOG.md` if behavior changed.
- Service docs/capabilities if support status or caveats changed.

## Handoff

Every session must leave enough state for another agent to resume. Include in the final response and service review file:

- GitHub issue number and lifecycle label.
- Operations reviewed.
- AWS docs used.
- Tests added or updated.
- Fixes made.
- Verification commands run.
- Remaining gaps.
- Exact next operation to review.

## Verification

For code changes, run scoped checks first:

```sh
go test -count=1 ./internal/services/<service>/... ./tests/integration/<service>/...
go vet ./internal/services/<service>/... ./tests/integration/<service>/...
```

Before final handoff for broad changes, run:

```sh
go build ./...
go vet ./...
```
