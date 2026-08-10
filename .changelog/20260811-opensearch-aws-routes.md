* [opensearch] every OpenSearch operation is served at the binding AWS models — the domain surface under `/2021-01-01/opensearch/`, and `ListDomainNames`, `AddTags`, `ListTags` and `RemoveTags` directly under `/2021-01-01/` — so an unmodified SDK, CDK construct or `aws opensearch …` call reaches it instead of answering 501
- [opensearch] the emulator-only `/_opensearch/*` path prefix and the invented `OpenSearch.` `X-Amz-Target` namespace, which duplicated every operation on a wire contract the pinned model gives OpenSearch no trace of
  migration: use AWS's own bindings — `POST /2021-01-01/opensearch/domain`, `GET` and `DELETE /2021-01-01/opensearch/domain/{DomainName}`, `POST /2021-01-01/opensearch/domain-info`, `GET /2021-01-01/domain`, `POST /2021-01-01/tags`, `GET /2021-01-01/tags?arn=…` and `POST /2021-01-01/tags-removal`
~! [opensearch] domains are scoped to the region they were created in, as on AWS, where one domain of a given name was shared by every region
  migration: domains recorded by an earlier version are not visible to this one; recreate them
*! [opensearch] `CreateDomain` answers `ResourceAlreadyExistsException` for a domain name already in use in the region, where a second create silently replaced the first
  migration: delete the existing domain, or create under a different name
+! [opensearch] `ListTags`, `AddTags`, `RemoveTags` and `DescribeDomains` require the members AWS marks required, answering `ValidationException` where an empty ARN or an empty domain list was accepted
  migration: pass the required member — `?arn=` on `ListTags`, `ARN` on `AddTags` and `RemoveTags`, and at least one name in `DescribeDomains`
~ [opensearch] `ResourceNotFoundException` carries HTTP 409, the status AWS documents for it, in place of 404, and a store failure is reported as OpenSearch's own `InternalException`
+ [opensearch] `ListDomainNames` honours the `engineType` query parameter, and a domain's `EngineType` follows from its `EngineVersion` instead of being reported as `OpenSearch` whatever engine it runs
+ [opensearch] `CreateDomain` applies an inline `TagList` at creation, so `ListTags` sees a tagged create without a second call
* [opensearch] `DeleteDomain` removes the domain's tags; they were written under the resource ARN and deleted by domain name, so a deleted domain left its tags behind for the next domain of that name to inherit
* [opensearch/cloudformation] `AWS::OpenSearchService::Domain` dispatches over the modeled REST binding and addresses the domain by name when deleting it, where it sent the ARN it uses as the physical ID and matched nothing
* [opensearch/router] OpenSearch requests are classified from the `/2021-01-01` path prefix, so they are logged and authorised as `opensearch` rather than falling through to S3 when unsigned
