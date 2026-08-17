# The unrecognised-filter rule, beyond EC2

> Status: survey complete, fixes **not** applied here. #1032 settled what an
> unrecognised filter means and applied it to EC2. This is the answer to the
> question that issue deliberately left open — whether anything else in Overcast
> has the same divergence — and what it is worth doing about it.
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

### 1. `ssm:DescribeParameters` — ignores the name, returns everything

`internal/services/ssm/handler.go`, `matchesFilters`:

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

This is EC2's `parseFilterValues` shape exactly, and it is the more dangerous of
the two failure modes. Three separate ways to get a confidently wrong answer:

- **An unimplemented `Key`** — `Type`, `KeyId`, `Path`, `Label`, `Tier`,
  `DataType` are all real AWS filter keys, and every one of them is ignored.
- **An unimplemented `Option` on an implemented `Key`** — `Key=Name` with
  `Option=Equals` or `Contains` falls through the inner `if` and is ignored, so
  a filter the caller reasonably believes is narrowing does nothing.
- **A missing `Option`** — AWS defaults `Option` to `Equals` for most keys, not
  `BeginsWith`, so `{Key: "Name", Values: ["/app/db"]}` with no `Option` is
  ignored here and would be an exact match on AWS.

AWS models this precisely: `InvalidFilterKey`, `InvalidFilterOption` and
`InvalidFilterValue` are all declared errors of `DescribeParameters`. So unlike
EC2, the honest answer here needs no judgement call about what to invent — the
error already exists in the API.

**Blast radius of fixing it:** small. `DescribeParameters` is a console and CLI
operation; CDK and CloudFormation read parameters through `GetParameter`, which
takes no filters.

### 2. `autoscaling:DescribeTags` — ignores the name, returns everything

`internal/services/autoscaling/typed_logic.go`, `describeTagsTyped`:

```go
for _, f := range req.Filters {
    if f.Name == "auto-scaling-group" && len(f.Values) > 0 {
        resourceFilter = f.Values[0]
    }
}
```

Same shape, plus two narrower bugs of its own that the same fix should settle:

- `key`, `value` and `propagate-at-launch` are real AWS filter names for this
  operation and all are ignored.
- Only `Values[0]` of `auto-scaling-group` is read, so a caller asking about
  three groups is answered about the first and told nothing about it.

**Blast radius:** very small. Nothing in the compat suites filters ASG tags.

### 3. `secretsmanager:ListSecrets` / `BatchGetSecretValue` — matches nothing

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

Ranked. The first two are the ones that can hand a caller a wrong answer.

1. **`ssm:DescribeParameters`** — refuse an unimplemented `Key` with
   `InvalidFilterKey` and an unimplemented `Option` with `InvalidFilterOption`,
   and implement `Equals` (AWS's default) alongside `BeginsWith`. The declared
   AWS errors mean this needs no invented behaviour.
2. **`autoscaling:DescribeTags`** — refuse an unimplemented filter name, honour
   every value of `auto-scaling-group` rather than the first, and implement
   `key`, `value` and `propagate-at-launch`, all of which are on the record
   already.
3. **`secretsmanager`** — optional, and lower value than it looks. Moving from
   "matches nothing" to `ValidationException` is a fidelity improvement with no
   wrong answers to prevent.

### Should `filterSpec` be shared?

**Not yet.** EC2's `filterSpec` is worth copying as a *pattern* — declare the
implemented names in one place, and let that declaration be the matcher, the
error and the documentation — but not yet worth extracting into `serviceutil`:

- The four APIs have four different request shapes. EC2 reads
  `Filter.N.Name`/`Filter.N.Value.M` off a Query form; SSM takes
  `ParameterFilters[].Key` plus an `Option` that has no EC2 equivalent;
  SecretsManager takes `Filters[].Key` and matches by case-insensitive prefix,
  not equality; AutoScaling takes `Filters[].Name` over a typed request. Only
  the *matching* generalises, and the matching is the small half.
- Each service's error differs — `InvalidParameterValue`, `InvalidFilterKey`,
  `ValidationException` — and the error is the point of the rule.
- Two call sites is not yet evidence of a shape. A third would be.

The cost of copying the pattern twice is a few dozen lines; the cost of the
wrong abstraction across four wire formats is paid by everyone who touches a
filter afterwards. Revisit when a fourth service grows a filter API, and keep
`internal/services/ec2/filters.go` as the worked example in the meantime.

### What this does not cover

Filter *values*. EC2 now reads `*` and `?` as AWS's wildcards; SSM, AutoScaling
and SecretsManager do not, and SecretsManager's prefix matching is its own
documented AWS behaviour rather than a gap. Each is a separate question from
"what does an unrecognised name mean", and none of them produces the
silently-wrong-answer failure this document is about.
