~ [docs] Confirm the Firehose, Glue, IAM, Kinesis, KMS, Lambda and MSK service docs, and fix the broken environment-variable reference links
  Lambda's concurrency, runtimes and landing pages pointed "every LAMBDA_* environment variable" at docs/configuration.md, which no longer lists any.
  Cut the design-rationale asides and an internal issue reference from the Lambda and IAM pages, and dropped the vestigial "Reaching real AWS" stub from lambda/examples.md.
  Separated the back-to-back callouts on glue.md and msk.md, and fixed the bare back-link openers on the IAM sub-pages.
