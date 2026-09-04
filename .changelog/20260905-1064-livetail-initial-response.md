* [cloudwatch-logs] `StartLiveTail` opens with the `initial-response` frame, so the AWS SDK for Go v2 gets a session instead of deadlocking (#1064).
  the AWS JSON protocols carry an operation's initial response document there, and smithy-go parks the deserializer on that frame before it yields the stream.
  `sessionStart` and `sessionUpdate` follow it unchanged, so the console's tail view and every JS SDK consumer see what they saw before.
+ [docs/cloudwatch-logs] The service page says how to reach `StartLiveTail` from Go, which needs an immutable endpoint.
  the Go SDK prefixes `streaming-` onto whatever host you configure, and only the legacy resolver can suppress it.
