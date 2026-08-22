* [sqs] `DeleteQueue`, `PurgeQueue`, `SetQueueAttributes`, `TagQueue` and
  `UntagQueue` no longer emit an empty `<{Action}Result></{Action}Result>`
  wrapper on the legacy Query/XML protocol (#1273). These five operations have
  no modeled output at all, and real AWS's Query/XML response for them goes
  straight from the opening `<{Action}Response>` element to
  `<ResponseMetadata>` with no result element in between. The JSON protocol
  was already correct (`{}` with no wrapper) and is unaffected.
