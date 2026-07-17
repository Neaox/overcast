# EventBridge — endpoint support

> AWS docs: [EventBridge API Reference](https://docs.aws.amazon.com/eventbridge/latest/APIReference/Welcome.html)

EventBridge accepts AWS JSON 1.1 via `X-Amz-Target: AWSEvents.<operation>`.
It also accepts Smithy RPC v2 CBOR at `/service/EventBridge/operation/<operation>`
with `Smithy-Protocol: rpc-v2-cbor` and `Content-Type: application/cbor`.
Overcast implements event buses, rules, targets, tagging, event ingestion, and
partial same-process target delivery.

> [!WARNING]
> **Emulation tier: Partial** — EventBridge matches common event patterns and invokes
> SQS targets for `PutEvents`; scheduled rules can invoke ECS/Fargate `RunTask` targets.
> Other target types and advanced pattern operators are still incomplete.

---

## Notes

- **Partial event routing.** `PutEvents` evaluates exact-match rule patterns and delivers
  matching events to SQS targets. Unsupported target types are skipped and logged.
- **Scheduled ECS targets.** Rate and basic AWS cron expressions are evaluated by an
  in-process clock-driven engine. ECS/Fargate targets call ECS `RunTask` with the
  configured target parameters.
- **Synthetic default bus.** `DescribeEventBus` returns a synthetic "default" bus even if one
  has not been explicitly created.
- **CDK compatible management plane.** Sufficient for CDK deployments that create buses,
  rules, and targets, including scheduled ECS/Fargate task target metadata.

<!-- BEGIN overcast:capabilities -->

## Summary

| Category    | ✅ Supported | ❌ Unsupported |
| ----------- | ------------ | -------------- |
| Event buses | 4            |                |
| Rules       | 6            |                |
| Targets     | 3            |                |
| Events      | 1            |                |
| Tags        | 2            | 1              |
| Archives    |              | 4              |
| Replays     |              | 3              |
| Connections |              | 4              |

---

## Endpoints

### Event buses

| Operation          | Status       | Notes                                      | AWS Docs                                                                                      |
| ------------------ | ------------ | ------------------------------------------ | --------------------------------------------------------------------------------------------- |
| `CreateEventBus`   | ✅ Supported | Creates a custom event bus                 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateEventBus.html)   |
| `DescribeEventBus` | ✅ Supported | Returns bus details; synthetic default bus | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeEventBus.html) |
| `ListEventBuses`   | ✅ Supported | Always includes default bus                | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListEventBuses.html)   |
| `DeleteEventBus`   | ✅ Supported |                                            | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteEventBus.html)   |

### Rules

| Operation      | Status       | Notes                       | AWS Docs                                                                                  |
| -------------- | ------------ | --------------------------- | ----------------------------------------------------------------------------------------- |
| `PutRule`      | ✅ Supported | Creates or updates a rule   | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutRule.html)      |
| `DescribeRule` | ✅ Supported |                             | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeRule.html) |
| `ListRules`    | ✅ Supported | Lists rules for a bus       | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListRules.html)    |
| `EnableRule`   | ✅ Supported | Sets rule state to ENABLED  | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_EnableRule.html)   |
| `DisableRule`  | ✅ Supported | Sets rule state to DISABLED | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DisableRule.html)  |
| `DeleteRule`   | ✅ Supported |                             | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteRule.html)   |

### Targets

| Operation           | Status       | Notes                                                              | AWS Docs                                                                                       |
| ------------------- | ------------ | ------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| `PutTargets`        | ✅ Supported | Adds targets; supports SQS delivery and scheduled ECS task targets | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutTargets.html)        |
| `ListTargetsByRule` | ✅ Supported | Lists targets including ECS target parameters                      | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListTargetsByRule.html) |
| `RemoveTargets`     | ✅ Supported | Removes targets from a rule                                        | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_RemoveTargets.html)     |

### Events

| Operation   | Status       | Notes                                                     | AWS Docs                                                                               |
| ----------- | ------------ | --------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `PutEvents` | ✅ Supported | Accepts events and delivers matching rules to SQS targets | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_PutEvents.html) |

### Tags

| Operation             | Status         | Notes                    | AWS Docs                                                                                         |
| --------------------- | -------------- | ------------------------ | ------------------------------------------------------------------------------------------------ |
| `TagResource`         | ✅ Supported   | Tag buses and rules      | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_TagResource.html)         |
| `ListTagsForResource` | ✅ Supported   | List tags for a resource | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListTagsForResource.html) |
| `UntagResource`       | ❌ Unsupported | Returns 501              | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_UntagResource.html)       |

### Archives

| Operation         | Status         | Notes       | AWS Docs                                                                                     |
| ----------------- | -------------- | ----------- | -------------------------------------------------------------------------------------------- |
| `CreateArchive`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateArchive.html)   |
| `DescribeArchive` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeArchive.html) |
| `ListArchives`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListArchives.html)    |
| `DeleteArchive`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteArchive.html)   |

### Replays

| Operation        | Status         | Notes       | AWS Docs                                                                                    |
| ---------------- | -------------- | ----------- | ------------------------------------------------------------------------------------------- |
| `StartReplay`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_StartReplay.html)    |
| `DescribeReplay` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeReplay.html) |
| `ListReplays`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListReplays.html)    |

### Connections

| Operation            | Status         | Notes       | AWS Docs                                                                                        |
| -------------------- | -------------- | ----------- | ----------------------------------------------------------------------------------------------- |
| `CreateConnection`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_CreateConnection.html)   |
| `DescribeConnection` | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DescribeConnection.html) |
| `ListConnections`    | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_ListConnections.html)    |
| `DeleteConnection`   | ❌ Unsupported | Returns 501 | [docs](https://docs.aws.amazon.com/eventbridge/latest/APIReference/API_DeleteConnection.html)   |

<!-- END overcast:capabilities -->
