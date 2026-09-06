*! [sqs] the three batch operations enforce AWS's entry limits, rejecting a whole batch rather than half-applying it.
  an empty batch, more than 10 entries, or duplicate entry `Id`s are refused with `EmptyBatchRequest`, `TooManyEntriesInBatchRequest` and `BatchEntryIdsNotDistinct`.
  migration: a client sending oversized or duplicate-id batches was silently succeeding locally and failing on AWS; chunk to 10 distinct-id entries per call.
+ [sqs] `SendMessage` and `SendMessageBatch` return `MD5OfMessageAttributes` when a message carries attributes.
* [sqs] `GetQueueAttributes` reports `CreatedTimestamp`, `LastModifiedTimestamp` and `ApproximateNumberOfMessagesDelayed`.
  `All` is also honoured from any position in the `AttributeNames` list, not only the first.
*! [sqs] `GetQueueAttributes` rejects an unmodelled attribute name with `InvalidAttributeName`.
  it used to answer with whatever else matched, so a typo read as "that attribute is empty".
  migration: a typo in an `AttributeNames` entry now fails the call; correct the name.
