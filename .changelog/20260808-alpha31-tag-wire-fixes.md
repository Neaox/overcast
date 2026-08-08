* [rds] AddTagsToResource parses the wire form real SDKs send (`Tags.Tag.N.Key`,
  the RDS model's locationName) instead of silently dropping every tag; the
  member-indexed form remains a fallback. Tag operations now reject resources
  that do not exist (DBInstanceNotFound, DBClusterNotFoundFault, …) instead of
  minting a tag store for any string
* [waf] TagResource accepts and ListTagsForResource returns Tags/TagList as
  lists of {Key,Value} structs, matching the WAFv2 wire format that SDK
  clients send — previously tag operations were unusable via the SDK
* [firehose] TagDeliveryStream accepts Tags as a list of {Key,Value} structs
  matching the real API instead of rejecting SDK requests with a 400;
  ListTagsForDeliveryStream serializes an untagged stream's Tags as [] rather
  than null
* [pipes/router] the shared /tags/{resourceArn} route space now has a single
  dispatching owner that routes by the ARN's service prefix, so Pipes tag
  operations reach Pipes instead of silently landing in the API Gateway or
  EKS tag stores; Pipes tag responses use the real API's lowercase `tags`
  member, validate tags, and answer NotFoundException for a missing pipe
* [eventbridge] tag operations no longer 500 (or panic) on a corrupt
  persisted tag blob, validate tags, surface store write failures, and answer
  ResourceNotFoundException for a rule or event bus that does not exist
* [athena] TagResource validates tags (limit and reserved-prefix checks) with
  InvalidRequestException, matching real Athena
* [ssm] tag operations answer a missing resource with InvalidResourceId
  instead of ParameterNotFound, and AddTagsToResource validates tags
  (TooManyTagsError above the 50-tag limit)
* [sns] tag operations answer a missing topic with error code ResourceNotFound
  as on real SNS; topic operations keep NotFound
* [serviceutil] a corrupt persisted tag blob reads as empty instead of turning
  every tag operation on that resource into a 500, for all services using the
  shared tag store helpers
