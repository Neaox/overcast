---
name: Endpoint implementation
about: Implement or expand support for an AWS-compatible endpoint
title: 'Implement endpoint: <service> <operation>'
labels: enhancement, area/emulation, needs-tests, status/ready
assignees: ''
---

## Scope

<!-- Identify the exact AWS operation and desired support level. -->

**Service:**
**Operation:**
**Protocol:** <!-- Query / JSON 1.1 / REST JSON / REST XML -->
**Support target:** <!-- minimal useful / partial / full documented behavior -->

## References

- AWS API docs:
- AWS examples:
- Similar Overcast implementation:
- Current docs/status:

## Acceptance Criteria

- [ ] Request parsing matches AWS wire format
- [ ] Success response shape matches AWS SDK expectations
- [ ] Documented validation and common error cases are covered
- [ ] State changes go through `state.Store` when persistence is required
- [ ] CloudFormation handler is added or explicitly not applicable
- [ ] Integration tests use SDK or wire-level HTTP where practical
- [ ] `docs/services/<service>.md` and status files are updated if support changes

## Test Plan

<!-- List the failing/reproducing tests to add first. -->

- [ ]

## Notes

<!-- Include constraints, edge cases, or non-goals. -->

<!-- Suggested labels: service/<name>, compat, aws-fidelity, priority/p2, effort/medium -->
