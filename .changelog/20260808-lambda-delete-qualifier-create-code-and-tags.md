*! [lambda] `DeleteFunction` honours `Qualifier` instead of ignoring it. A qualified request now
  deletes only that published version, along with its qualified resource policy and provisioned
  concurrency, and leaves `$LATEST`, the function record, other versions, aliases and unqualified
  policies alone. `$LATEST` is refused with `InvalidParameterValueException`, a version an alias
  still points at with `ResourceConflictException`, and an unknown version or alias with
  `ResourceNotFoundException`; an unqualified request still deletes the function. The qualifier may
  also travel in the function name, as `my-function:2`
  migration: any caller that passed `Qualifier` to `DeleteFunction` expecting the whole function to
  go must drop the qualifier — it used to be ignored, so those calls deleted more than they asked for
*! [lambda] `CreateFunction` fails when its `Code.S3Bucket`/`Code.S3Key` deployment package cannot
  be fetched, returning AWS's `InvalidParameterValueException` GetObject translation and persisting
  nothing. It used to log a warning and create a function with no usable code, which then failed at
  invoke time with an unrelated message. `AWS::Lambda::Function` surfaces the same failure through
  ordinary stack rollback
  migration: a stack or script that relied on the function being created before its package was
  uploaded must upload the object first — CloudFormation now rolls the stack back instead
*! [lambda] the tag operations take a `TaggableResource` ARN, as AWS models them. A bare function
  name, a partial ARN or another service's ARN is refused with `InvalidParameterValueException`
  rather than treated as a function name, and a version- or alias-qualified ARN is refused too
  because Lambda tags only the unqualified function. `TagResource` requires `Tags` and
  `UntagResource` requires `tagKeys`; a resource in another region or account reports
  `ResourceNotFoundException`
  migration: callers passing a bare function name to `TagResource`, `UntagResource` or `ListTags`
  must pass the function ARN
+ [lambda] `TagResource`, `UntagResource` and `ListTags` work on event source mappings. Their tags
  are stored separately from the mapping and are deleted with it, so they never appear in
  `EventSourceMappingConfiguration`, which has no `Tags` member. Code signing configurations,
  capacity providers and network connectors still return `501`
+ [web] the Lambda function page has a Tags card that reads and writes through
  `ListTags`/`TagResource`/`UntagResource`, and the Versions tab can delete a single published
  version. The delete-function confirmation now says that versions and aliases go with it
