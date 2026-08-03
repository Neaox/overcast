* [sqs] A Query-protocol request addressed by the queue's own URL — `POST
  /<account>/<queue>` with `Action=SendMessage` and no `QueueUrl` parameter, the
  shape AWS's API Reference documents — no longer fails with `MissingParameter`.
  The queue now comes from the request path when the body omits it. A `QueueUrl`
  in the body still wins, and a request to `POST /` without one is still the
  client error it always was.
* [sqs] `ListQueueTags` renders a tag as `<Tag><Key>…</Key><Value>…</Value>`
  under the Query protocol, matching AWS and the rest of Overcast. It was
  emitting `<Name>` — the shape that belongs to `Attribute`, not `Tag`.
