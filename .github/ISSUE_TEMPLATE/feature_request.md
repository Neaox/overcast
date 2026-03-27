---
name: Feature request
about: Request a new endpoint, service, or capability
title: '[FEATURE] <service>: <operation or feature>'
labels: enhancement, needs-triage
assignees: ''
---

## What would you like

<!-- Describe the feature. Be as specific as possible about the endpoint or behaviour. -->

## Which service and operation

<!-- e.g. S3 / GetBucketVersioning, DynamoDB / TransactWriteItems, new service: Kinesis -->

**Service:**
**Operation(s):**
**AWS docs link:** <!-- https://docs.aws.amazon.com/... -->

---

## Why do you need it

<!-- What are you trying to build or test? Understanding the use case helps us prioritise. -->

## Current workaround

<!-- If you have a workaround, describe it. If not, explain the impact. -->

## Would you be willing to implement this?

- [ ] Yes, I'd like to implement this and submit a PR
- [ ] I could help with tests / review
- [ ] No, but I'd love to see it land

---

## Acceptance criteria

<!-- What does "done" look like? List the specific behaviours that must be true. -->

- [ ] `<Operation>` returns HTTP `<status>` with `<field>` set to `<value>`
- [ ] Error case: `<condition>` returns `<ErrorCode>`
- [ ] `docs/services/<service>.md` updated to ✅ Supported

---

<!--
Priority labels (add one):
  P1 - Blocks a core use case; many users affected
  P2 - Important but has a workaround
  P3 - Nice to have / edge case

Effort labels (added by maintainers after triage):
  effort:small  - <2h (single endpoint, simple response)
  effort:medium - 2–8h (multiple endpoints, or expression parsing required)
  effort:large  - >8h (new service, complex state, or event pipeline)
-->
