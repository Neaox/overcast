* [sqs] JSON-protocol error responses now carry x-amzn-query-error, matching AWS's aws.protocols#awsQueryCompatible behaviour
  e.g. GetQueueUrl on a missing queue sends `AWS.SimpleQueueService.NonExistentQueue;Sender` alongside the JSON body's own `__type`
  fixes #1810
