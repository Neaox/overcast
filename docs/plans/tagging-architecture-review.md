# Tagging Architecture Review

> Status: review complete. The EC2 findings are **fixed** on this branch; the
> cross-service findings are **reported, not fixed**, and are listed under
> [What remains](#what-remains).
>
> This is the *architecture* axis of tagging — how tags are stored, read, kept
> and rendered. The *coverage* axis — which resources can be tagged at all — is
> [resource-tagging-coverage.md](./resource-tagging-coverage.md), and the two do
> not overlap: a service can appear complete there and still be wrong here.

## Why this review happened

A user's `overcast.sh start` failed with CDK's `LambdaFunctionsPublicSubnet`
error. The script find-or-creates its own VPC:

```sh
VPC_ID=$(awslocal ec2 describe-vpcs --filters "Name=tag:Name,Values=$LOCAL_VPC_NAME_TAG" --query 'Vpcs[0].VpcId' --output text)
if [ "$VPC_ID" = "None" ]; then  # ...create a 10.42.0.0/16 VPC...
else                             # ...reuse $VPC_ID...
fi
```

`DescribeVpcs` did not implement the `tag:Name` filter and ignored it silently,
so the call returned every VPC in the region. `Vpcs[0]` was the seeded default
VPC — which, like a real default VPC, has an internet gateway and a `0.0.0.0/0`
route, so CDK correctly classified its subnets as public. The `else` branch ran
every time and the script never created the private VPC it was written to use.

Nothing here is a CDK bug and nothing is a bug in the script. The emulator was
asked a question, could not answer it, and answered anyway.

## Method

1. The authority for behaviour is handler and store code, not the capability
   declarations or `docs/services/*.md`, both of which have been found to
   overstate reality before (issue #794).
2. Every service under `internal/services/` was searched for tag storage, tag
   reads, tag rendering and tag-based filtering, and for whether a resource's
   delete path removes its tags.
3. Counts below are call sites in non-test code, measured on this branch's
   merge base.

## Findings

### 1. The shared helper is four helpers wearing one coat

`internal/serviceutil/tags.go` offers **three independent families** for what is
fundamentally one job — merge tags in, take tags out, list them — across two
storage strategies (a separate namespace, or inline on the resource record):

| Family | Entry points | Services |
| --- | --- | --- |
| Direct store access | `ApplyTagsToStore`, `RemoveTagsFromStore`, `TagsFromStore` | ecs, eks, elasticache, lambda |
| `TagStore` interface | `NSStore` + `ApplyStoreTags`, `RemoveStoreTags`, `ListStoreTags` | appconfig, cloudwatch, eks, elasticache, eventbridge, lambda, msk, opensearch, rds, scheduler |
| Inline on the record | `ApplyInlineTags`, `RemoveInlineTags`, `ListInlineTags` | cognito, dynamodb, firehose, glue, ses, sns, stepfunctions |
| Inline, second generic | `ApplyTags`, `RemoveTags` | waf, and only waf |

The first two families do the *same thing to the same storage shape* and differ
only in signature: one takes `[]TagPair` and a `state.Store` plus a namespace,
the other takes `map[string]string` and a `TagStore`. **`eks`, `elasticache` and
`lambda` each use both of them**, so a single service reaches the same namespace
through two different abstractions.

The last two are also the same thing: two generics over `Taggable` differing in
their type-parameter shape and in `[]TagPair` vs `map[string]string`. One has
seven consumers, the other has one. The file's own section headers concede the
position — *"for services preferring abstraction over direct store access"* and
*"Legacy inline helpers"* — which is preference and history, not a design.

Five `Apply*`, four `Remove*` and three `List*` functions is not a shared helper
that services depend on; it is a menu. The cost is not the duplicated lines, it
is that no reviewer can say what the right one is, so the next service adds a
sixth.

**Beyond it, 22 services do not use any of them** — acm, apigateway,
appregistry, appsync, athena, cloudformation, cloudfront, cloudtrail, ec2, ecr,
efs, elbv2, iam, kinesis, kms, pipes, route53, secretsmanager, shield, sqs, ssm,
transfer. Some use `ValidateTags` while hand-rolling storage; some do neither.

### 2. Tags outlive the resources they describe

Namespaced tags are keyed by resource ID in a namespace of their own, so nothing
ties them to the record's lifetime, and `serviceutil` offers **no delete helper
at all** — there is no `RemoveAllTags`/`DeleteTags` counterpart to `ApplyTagsToStore`.
Cleanup is therefore something each service has to remember by hand, and the
split is roughly even:

- **Cleans up:** acm, appconfig, autoscaling, cloudwatch, ecs (tasks), efs, eks,
  lambda (event source mappings), msk, opensearch, route53.
- **Leaks:** eventbridge, elasticache, rds, scheduler — and ec2, until this
  branch.

EventBridge is the clearest illustration that this is an oversight rather than a
decision: `DeleteRule` carefully removes `nsRules`, `nsTargets`, `nsLastFire` and
`nsNextFire`, and leaves `eb:tags` behind.

The consequences are that `ListTagsForResource`-style operations keep reporting
a deleted resource's tags, and the store grows for the life of the session.
Services storing tags **inline** on the record (family 3) are immune by
construction, which is the one real argument in that family's favour.

### 3. Write path and read path can disagree — the EC2 worked example

EC2 stored tags in two places and read whichever one the handler's author had in
mind:

- `TagSpecification.N` on a create call was written to a `Tags` field on the
  resource's own record (`Instance`, `NatGateway`, `VpnGateway`,
  `VpcPeeringConnection`).
- `CreateTags`/`DeleteTags` wrote to the `ec2:tags` namespace.

Neither path read the other. So tagging an existing NAT gateway succeeded and
was invisible to `DescribeNatGateways`; an instance's create-time tags were
invisible to `DescribeTags`; and `VpcPeeringConnection.Tags` was written by
nothing and read by nothing at all. `DescribeVpcs` was a third variant again —
it ignored both and rendered a single synthetic `overcast:network-status` tag,
discarding whatever the caller had set. That last one is what made the reported
bug unworkable from the client side too: the obvious workaround for a missing
server-side filter is to filter client-side on the returned tags, and the tags
were not in the response to filter on.

### 4. Tag filters, and two opposite wrong answers

No EC2 `Describe*` implemented `tag:<key>`, `tag-key` or `tag-value`. Worse, the
package held two filter idioms that disagreed about an unrecognised filter:

- `parseFilterValues` (vpcs, subnets, security groups, route tables, …) looked
  up filters **by name** and never saw the others, so an unknown filter was
  ignored and the call returned **everything**.
- `matchFilters` (nat gateways, vpn gateways, network interfaces) compared every
  supplied filter against an attribute map and returned `false` on a name it did
  not know, so an unknown filter returned **nothing**.

Both are wrong, in opposite directions, in one service. Real AWS rejects an
unrecognised filter name with `InvalidParameterValue`.

### 5. Tag output order is not stable

Tags are stored as `map[string]string` and most renderers range over the map
straight into the response. Go randomises map iteration, so a client polling a
describe gets a different tag order on every call — churn for anything diffing
two responses, and untestable against a golden file. Only 8 services order their
tags before rendering; there are 19 render sites that do not. The same class of
bug was found and fixed in ACM's `ListTagsForCertificate` during the coverage
audit, which suggests it is worth fixing by construction rather than one report
at a time.

### 6. Validation is opt-in, and its charset rules do not exist

`ValidateTags` checks tag count and key/value lengths and the reserved `aws:`
prefix. 20 services define a `TagValidationConfig` and use it. EC2 — among
others — does not validate at all, so it accepts a 300-character key that AWS
would reject.

Even for the services that do validate, `internal/serviceutil/validation.go:267`
carries a standing `TODO(priority:P2)`: no charset pattern is enforced anywhere,
so invalid characters are accepted by every service sharing the validator.

## What this branch changed

Scope was held to EC2, the service the report came from. All of it is covered by
tests in `internal/services/ec2/tags_test.go`, which fail on the merge base.

- **One tag store.** `ec2:tags` is the only writer and the only reader. Create
  paths (`RunInstances`, `CreateNatGateway`, `CreateVpnGateway`) write their
  `TagSpecification.N` tags through the same helper `CreateTags` uses, and the
  four dead inline `Tags` fields are gone. `DescribeTags` now sees create-time
  tags, and a describe now sees `CreateTags`.
- **Tag filters, once.** `tag:<key>`, `tag-key` and `tag-value` are parsed in
  one place and applied through one `tagView.keep` call per describe, so "what
  tags does this resource have" and "does it pass the filters" cannot drift
  apart across handlers. They now work on every EC2 describe that returns a
  taggable resource: VPCs, subnets, instances, NAT gateways, VPN gateways,
  security groups, route tables, internet gateways and network interfaces —
  the last four of which did not return a `tagSet` at all before. Values match
  exactly; AWS's `*`/`?` wildcards are **not** implemented and are documented
  as such at the type.
- **The two opposite wrong answers are gone.** `matchFilters` skips tag
  selectors rather than rejecting them as unknown names, which it did — routing
  a tag filter through it returned *nothing*, the mirror image of the reported
  bug and a live regression while this change was half-applied. It is caught by
  `TestTagFilterWorksOnAttributeMapHandlers`.
- **VPC tags are the caller's again.** `DescribeVpcs` returns stored tags, with
  `overcast:network-status` merged in alongside rather than replacing them.
- **Tags die with their resource.** `ec2Store.deleteTags` is called by all 11
  `delete*` methods. Terminated instances keep their tags, as on AWS, because
  EC2 does not delete the instance record.
- **Stable order.** Tags are rendered key-sorted everywhere in the service.
- **One parser, one filter walk.** The two near-identical `TagSpecification.N`
  parsers noted in the coverage audit are now one; the three identical XML tag
  types are now one; and filter parsing is a single indexed pass rather than a
  map rescan per filter (see below).

### Performance

The naive shape of this fix reads a resource's tags inside the describe loop,
turning one store read into one per resource. Instead the region's tags are read
**once per request** — `SQLiteStore.Scan` over `(namespace, key)`, which is the
primary key, so it is a single bounded index range scan — and indexed in memory.
Describes go from `1 + N` reads to `1 + 1`. The read is skipped entirely when a
request neither filters on tags nor renders them.

Two incidental wins on the parsing side: `collectFormFilters` rescans the entire
form map for each filter it finds, costing filters × parameters rather than
parameters; and `DescribeVpcs` walked the filter list three times, once per
named filter. Tag filters are now parsed in a single indexed pass. (The
attribute-map handlers still use `collectFormFilters`; converging them is part
of item 2 under [What remains](#what-remains).)

## What remains

Ranked by how likely each is to produce a wrong answer rather than untidy code.
Items 1 and 3–5 are tracked in **#1031**; item 2 has its own issue, **#1032**,
because it is a behavioural decision rather than a defect to clear.

1. **Close the tag leak in eventbridge, elasticache, rds and scheduler.** The
   same one-line-per-delete fix this branch applied to EC2. Best done by giving
   `serviceutil` the delete helper it lacks, so the next service gets it free.
2. **Decide what an unrecognised filter means, and make every service agree.**
   AWS answers `InvalidParameterValue`. Overcast currently answers "everything"
   or "nothing" depending on which helper the handler happened to use. This is
   the root of the reported bug: implementing `tag:Name` fixes *this* filter,
   while the next unimplemented one fails exactly the same way. Erroring is the
   honest option and matches AGENTS.md's "a 501 with an honest explanation is
   better than a divergent 200", but it is a real behavioural change — a filter
   AWS models and Overcast does not implement would go from silently wrong to
   loudly refused, which could break a working CDK lookup. It needs a
   per-operation supported-filter list and its own review; it is deliberately
   **not** done here. Tracked in #1032.
3. **Collapse the four helper families to one per storage strategy.** Two
   strategies genuinely exist (namespaced, inline) and both are legitimate.
   Four entry-point sets for them are not. Retire the `waf`-only generic first —
   it has two call sites — then converge families 1 and 2, which differ only in
   signature, and move the three services using both onto the survivor.
4. **Make ordered output the default.** Have the shared list/render helper
   return key-sorted tags so a service cannot render an unordered map by
   accident, rather than fixing 19 sites individually.
5. **Give EC2 tag validation**, and resolve the standing charset `TODO` in
   `ValidateTags` so the services that do validate enforce what their model
   says.
