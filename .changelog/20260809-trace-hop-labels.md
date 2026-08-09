* [debug/s3] trace hops for S3 calls name the service instead of showing a
  blank one — S3's REST paths are bare bucket paths, which no prefix rule can
  attribute
* [debug/cloudformation] a stack created from a `TemplateURL` records the
  template fetch as a hop, and the S3 request it makes links back to the
  CloudFormation request that triggered it
* [debug] Query-protocol requests are named by their operation however the
  client ordered the form parameters; one with a large parameter ahead of
  `Action` — a `CreateStack` carrying a real template, typically — used to
  show a blank operation
