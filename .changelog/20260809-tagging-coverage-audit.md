+ [sns] `CreateTopic` applies inline `Tags` at creation, as AWS does; a repeat idempotent call leaves an existing topic's tags untouched
+ [kinesis] `TagResource`, `UntagResource` and `ListTagsForResource` — the ARN-addressed tag operations the AWS CLI's `kinesis tag-resource` uses. They read and write the same tag set as `AddTagsToStream`
+ [kinesis] `CreateStream` applies inline `Tags` at creation
+ [ses] SESv2 resource tagging: `TagResource`, `UntagResource` and `ListTagsForResource` on `/v2/email/tags`, for email identities. `CreateEmailIdentity` applies inline `Tags` at creation, `GetEmailIdentity` reports them, and deleting an identity drops them
+ [transfer] Transfer Family servers and users are taggable: `TagResource`, `UntagResource` and `ListTagsForResource`, plus inline `Tags` on `CreateServer` and `CreateUser`. `DescribeServer` and `DescribeUser` report them
+ [cloudtrail] Trails are taggable: `AddTags`, `RemoveTags` and `ListTags`, plus an inline `TagsList` on `CreateTrail`
+ [iam] Managed policies and instance profiles are taggable: `TagPolicy`/`UntagPolicy`/`ListPolicyTags` and `TagInstanceProfile`/`UntagInstanceProfile`/`ListInstanceProfileTags`
+ [iam] `CreateUser`, `CreateRole`, `CreatePolicy` and `CreateInstanceProfile` apply inline `Tags` at creation
