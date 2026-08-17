# The unrecognised-filter rule, beyond EC2

> Status: survey complete, and the two findings that could produce a wrong
> answer are **fixed** — `ssm:DescribeParameters` and `autoscaling:DescribeTags`.
> The third, `secretsmanager`, is a deliberate divergence that fails safe and is
> left alone; see [What to do](#what-to-do).
>
> #1032 settled what an unrecognised filter means and applied it to EC2. This is
> the answer to the question that issue deliberately left open — whether
> anything else in Overcast has the same divergence.
>
> Parent review: [tagging-architecture-review.md](./tagging-architecture-review.md),
> item 2 of "What remains".

## The rule, restated

A caller supplies a named selector. The handler either implements that name or
does not. If it does not, there are three things it can do, and only one of them
is honest:

| Answer | What the caller sees |
| --- | --- |
| Ignore the name | Every resource, presented as a filtered result — **a wrong answer** |
| Match nothing | An empty result, presented as "no match" — wrong, but fails safe |
| Refuse the name | An error naming the filter — what AWS does |

EC2 answered the first two, in different handlers, which is what #1032 was
filed about: `describe-vpcs --filters Name=tag:Name,...` was ignored, the caller
read `Vpcs[0]`, and a find-or-create script adopted the wrong VPC.

## Method

Every package under `internal/services/` was searched for a caller-supplied
named-selector API — `Filters`, `ParameterFilters`, `TagFilters` — and each
match read to find what it does with a name it has no case for. Handler code is
the authority, not the capability declarations. Read on the merge base of this
branch.

Most services have none: they select by ID, by prefix, or not at all. Four have
one, and they do not agree.

## Findings

### 1. `ssm:DescribeParameters` — ignored the name, returned everything — **fixed**

As it was, in `internal/services/ssm/handler.go`'s `matchesFilters` — and again,
verbatim, in `typed_logic.go`'s `matchesTypedFilters`:

```go
for _, f := range filters {
    if f.Key == "Name" {
        if f.Option == "BeginsWith" {
            // ...the only filter that is applied
        }
    }
}
return true
```

That is EC2's `parseFilterValues` shape exactly, and the more dangerous of the
two failure modes. There were three separate ways to get a confidently wrong
answer:

- **An unimplemented `Key`** — `Type`, `KeyId`, `Path`, `Label`, `Tier`,
  `DataType` are all real AWS filter keys, and every one of them was ignored.
- **An unimplemented `Option` on an implemented `Key`** — `Key=Name` with
  `Option=Equals` or `Contains` fell through the inner `if` and was ignored, so
  a filter the caller reasonably believed was narrowing did nothing.
- **A missing `Option`** — AWS defaults `Option` to `Equals` for most keys, not
  `BeginsWith`, so `{Key: "Name", Values: ["/app/db"]}` with no `Option` was
  ignored here and is an exact match on AWS.

AWS models this precisely: `InvalidFilterKey`, `InvalidFilterOption` and
`InvalidFilterValue` are all declared errors of `DescribeParameters`. So unlike
EC2, the honest answer here needed no judgement call about what to invent — the
error already existed in the API.

**Blast radius:** small. `DescribeParameters` is a console and CLI operation;
CDK and CloudFormation read parameters through `GetParameter`, which takes no
filters.

### 2. `autoscaling:DescribeTags` — ignored the name, returned everything — **fixed**

As it was, in `internal/services/autoscaling/typed_logic.go`'s
`describeTagsTyped`:

```go
for _, f := range req.Filters {
    if f.Name == "auto-scaling-group" && len(f.Values) > 0 {
        resourceFilter = f.Values[0]
    }
}
```

Same shape, plus two narrower bugs of its own that the same fix settled:

- `key`, `value` and `propagate-at-launch` are real AWS filter names for this
  operation and all were ignored.
- Only `Values[0]` of `auto-scaling-group` was read, so a caller asking about
  three groups was answered about the first and told nothing about the others.

**Blast radius:** very small. Nothing in the compat suites filters ASG tags.

### 3. `secretsmanager:ListSecrets` / `BatchGetSecretValue` — matches nothing — left as is

`internal/services/secretsmanager/handler.go`, `secretMatchesFilter`, already
ends:

```go
default:
    // An unknown key matches nothing rather than everything: a filter the
    // emulator does not understand must not silently widen the result.
    return false
```

This is EC2's `matchFilters` direction — but unlike EC2's, it is deliberate,
commented, and reasoned from the same principle #1032 landed on. It is still a
divergence (AWS answers `ValidationException` for an invalid filter key) and it
still costs a caller time, because an empty list looks like an empty account.
It is the least urgent of the three: it fails safe.

### 4. Everything else — no named-selector API

`rds`, `elbv2`, `ecs`, `eks`, `dynamodb`, `efs`, `elasticache` and the rest
either select by identifier, by prefix, or expose no filter parameter at all.
`lambda`'s `FilterCriteria` and `pipes`' filter patterns are **event-source
filtering** — a different thing entirely, matching records rather than selecting
resources, with its own AWS-defined pattern language that is already
implemented. `cloudformation`'s `TagFilters` are S3 lifecycle-rule properties
being translated, not a query API.

## What to do

Ranked when this was written. The first two could hand a caller a wrong answer,
and both are now done.

1. ~~**`ssm:DescribeParameters`**~~ **Done.** An unimplemented `Key` is refused
   with `InvalidFilterKey` and an unimplemented `Option` with
   `InvalidFilterOption` — both declared errors of the operation, so no
   behaviour had to be invented. `Equals` (AWS's default for an omitted
   `Option`, and previously the comparison that matched everything) and
   `Contains` are implemented alongside `BeginsWith`, and `Type` alongside
   `Name`. The legacy handler and the typed path now validate and match through
   one declaration in `internal/services/ssm/filters.go` rather than two copies
   of the same eight lines — which is how EC2's two idioms started.
2. ~~**`autoscaling:DescribeTags`**~~ **Done.** All four filter names AWS
   documents are implemented, a name outside them is refused with
   `ValidationError`, and every value of `auto-scaling-group` is honoured rather
   than only the first. The single-group case still answers from the tag key's
   prefix scan; several groups fall back to a full scan.
3. **`secretsmanager`** — **not done, deliberately.** Moving from "matches
   nothing" to `ValidationException` is a fidelity improvement with no wrong
   answers to prevent: the existing behaviour already refuses to widen a result,
   it is commented with that reasoning, and it fails safe. It is worth doing the
   next time someone is in that file, and is not worth a change of its own.

### Should `filterSpec` be shared?

**Not yet — and having now written it twice more, still not yet.** EC2's
`filterSpec` is worth copying as a *pattern* — declare the implemented names in
one place, and let that declaration be the matcher, the error and the
documentation — but not worth extracting into `serviceutil`:

- The four APIs have four different request shapes. EC2 reads
  `Filter.N.Name`/`Filter.N.Value.M` off a Query form; SSM takes
  `ParameterFilters[].Key` plus an `Option` that has no EC2 equivalent;
  SecretsManager takes `Filters[].Key` and matches by case-insensitive prefix,
  not equality; AutoScaling takes `Filters[].Name` over a typed request. Only
  the *matching* generalises, and the matching is the small half.
- Each service's error differs — `InvalidParameterValue`, `InvalidFilterKey`,
  `ValidationException` — and the error is the point of the rule.
- Two call sites is not yet evidence of a shape. A third would be.

What the SSM and AutoScaling fixes actually shared with EC2 was the *shape* — a
map from implemented name to accessor, validated before the scan and matched
after it — and each landed in about thirty lines. What they did not share was
the parsing, the option dimension SSM has and nothing else does, or the error.
A helper covering all three would be mostly parameters.

The cost of copying the pattern is a few dozen lines a service; the cost of the
wrong abstraction across four wire formats is paid by everyone who touches a
filter afterwards. Revisit when a fifth service grows a filter API, and keep
`internal/services/ec2/filters.go` as the worked example in the meantime.

### What this does not cover

Filter *values*. EC2 now reads `*` and `?` as AWS's wildcards; SSM, AutoScaling
and SecretsManager do not, and SecretsManager's prefix matching is its own
documented AWS behaviour rather than a gap. Each is a separate question from
"what does an unrecognised name mean", and none of them produces the
silently-wrong-answer failure this document is about.
