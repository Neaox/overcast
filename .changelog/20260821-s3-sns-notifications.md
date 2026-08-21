* [s3/sns] S3 bucket notifications now deliver to SNS topics. `TopicConfigurations` was accepted,
  stored and returned, but the notification dispatcher never read it, so a configured S3 → SNS
  destination silently never fired — a success response, a correct-looking round trip, and no
  delivery. Matching events now publish to the named topic through SNS's own fan-out, so the whole
  S3 → SNS → SQS/Lambda/email path works; event-type selection and prefix/suffix filters share the
  same matcher as the SQS and Lambda destinations. The envelope matches real S3: the
  `{"Records":[…]}` JSON travels as the SNS notification's `Message` string with subject
  "Amazon S3 Notification", so a queue subscribed to the topic receives the standard SNS envelope
  whose Message parses back into the S3 event.
