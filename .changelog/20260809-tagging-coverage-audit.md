+ [sns] `CreateTopic` applies inline `Tags` at creation, as AWS does; a repeat idempotent call leaves an existing topic's tags untouched
+ [kinesis] `TagResource`, `UntagResource` and `ListTagsForResource` — the ARN-addressed tag operations the AWS CLI's `kinesis tag-resource` uses. They read and write the same tag set as `AddTagsToStream`
+ [kinesis] `CreateStream` applies inline `Tags` at creation
