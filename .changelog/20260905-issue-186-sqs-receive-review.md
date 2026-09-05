* [sqs] a FIFO `ReceiveMessage` batch drains a whole message group instead of one message per group.
  `MaxNumberOfMessages=10` against a five-message group returned one message; it now returns all five in sequence order and fills the batch from other unblocked groups, as AWS does.
