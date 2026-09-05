~! [dynamodb] `UpdateTimeToLive` is asynchronous, as on AWS: TTL changes pass through `ENABLING`/`DISABLING` before they settle.
  `DescribeTimeToLive` reports all four `TimeToLiveStatus` values, and the sweeper expires items only once the status is `ENABLED`.
  a second `UpdateTimeToLive` inside the window, re-enabling an `ENABLED` table, or disabling a `DISABLED` one is now a `ValidationException`, matching AWS.
  both `TimeToLiveSpecification` members are required (`AttributeName` 1..255), and `AWS::DynamoDB::Table` refuses an attribute rename while TTL is enabled.
  migration: change a TTL attribute name by disabling TTL, waiting for `DISABLED`, then enabling it again — the same two-step AWS requires.
