# The unrecognised-filter rule, beyond EC2

> Status: **closed** — every finding is fixed. The wrong-answer direction
> (`ssm:DescribeParameters`, `autoscaling:DescribeTags`) was fixed first, as
> this survey was corrected; the three fail-safe findings this doc had
> deliberately deferred (`secretsmanager`, `stepfunctions:ListExecutions`,
> `cloudformation:ListStacks`) were fixed under #1188. See
> [What to do](#what-to-do).
>
> #1032 settled what an unrecognised filter means and applied it to EC2. This is
> the answer to the question that issue deliberately left open — whether
> anything else in Overcast has the same divergence.
>
> Parent review: the tagging architecture review (closed — every finding fixed
> via #1033/#1037/#1038/#1040/#1041; its plan doc was deleted 2026-08-21), which
> requested this survey as item 2 of its "What remains".

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
filter parameter, and each match read to find what it does with a value it has
no case for. Handler code is the authority, not the capability declarations.

The first pass of this survey searched only for the `Filters` /
`ParameterFilters` / `TagFilters` shape and concluded that four services had
one. That was too narrow, and the conclusion was wrong: a filter does not have
to be a list of name/value pairs. The search is now over every filter-shaped
request field — `grep -rn 'json:"[A-Za-z]*Filters\?"' internal/services/*/*.go`
— which finds three more, and the corrected list is below. The narrow search is
recorded here rather than quietly replaced because it is the reason findings 4
to 6 were missed the first time.

Seven services expose a filter, in three shapes:

| Shape | Services | Unrecognised input means |
| --- | --- | --- |
| Named selector (`Name`/`Key` → attribute) | ec2, ssm, autoscaling, secretsmanager | findings 1–3, all settled |
| Query-language string | cognito | finding 4 — already refuses |
| Enumerated value | stepfunctions, cloudformation | findings 5–6 — fixed under #1188 |

Only the first shape produced the wrong-answer failure this document was opened
about, and all of it is now closed. The fail-safe findings (3, 5, 6) are closed
too, under #1188.

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

### 3. `secretsmanager:ListSecrets` / `BatchGetSecretValue` — matched nothing — **fixed**

`internal/services/secretsmanager/handler.go`'s `secretMatchesFilter` used to
end:

```go
default:
    // An unknown key matches nothing rather than everything: a filter the
    // emulator does not understand must not silently widen the result.
    return false
```

This was EC2's `matchFilters` direction — but unlike EC2's, it was deliberate,
commented, and reasoned from the same principle #1032 landed on. It was still a
divergence (AWS answers `InvalidParameterException` for an invalid filter
key — that operation's own declared error, not the generic
`ValidationException`) and it cost a caller time, because an empty list looked
like an empty account.

Fixed under #1188: `validateSecretFilters` refuses a `Filter.Key` outside
AWS's `FilterNameStringType` enum — `description`, `name`, `tag-key`,
`tag-value`, `primary-region`, `owning-service`, `all` — before either
operation scans the store. `primary-region` and `owning-service` are accepted
but still match nothing, because Overcast does not model secret replication
or AWS-managed secrets; that is a coverage gap, not a validation one, and
still out of scope.

### 4. `cognito:ListUsers` — refuses already, and did before any of this

`internal/services/cognito/store.go`, `filterUsers`:

```go
attr, op, value, ok := parseListUsersFilter(filter)
if !ok || !listUsersFilterAttribute(attr) {
    return nil, &protocol.AWSError{Code: "InvalidParameterException", Message: "Invalid search filter.", HTTPStatus: 400}
}
```

Cognito's filter is a query-language string (`email ^= "a@b"`), not a
name/value pair, which is why the first pass of this survey did not find it. It
is the one place in Overcast that already answered the way #1032 concluded
everything should: an attribute outside the implemented set is refused, before
any user is read, with the error AWS uses.

Nothing to do, and worth knowing it exists — it is the precedent the EC2 rule
turned out to be catching up with. Two open issues, **#100** and **#131**, track
whether the *message* matches real AWS exactly; that is a parity question about
wording, not about what an unrecognised filter means.

### 5. `stepfunctions:ListExecutions` — an unknown `statusFilter` matched nothing — **fixed**

`execution_ops.go` compared `req.StatusFilter` to each execution's status
directly, so a value outside the enum returned an empty list where AWS returns
`ValidationException` — the enum is a model constraint there, and
`ValidationException` is one of `ListExecutions`' own declared errors. Fixed
under #1188: `req.StatusFilter` is validated against the six-value
`ExecutionStatus` enum (`RUNNING`, `SUCCEEDED`, `FAILED`, `TIMED_OUT`,
`ABORTED`, `PENDING_REDRIVE`) before the store is scanned.

### 6. `cloudformation:ListStacks` — an unknown `StackStatusFilter` matched nothing — **fixed**

`filterStacksByStatus` is `slices.Contains` over the supplied statuses, so the
same applied: an invalid status narrowed to nothing rather than being refused.
Fixed under #1188, and it needed the same judgement call EC2's original fix
did: Overcast's own `StackStatus` constants only cover the statuses its
provisioner produces, missing three of the `IMPORT_*` family
(`IMPORT_IN_PROGRESS`, `IMPORT_ROLLBACK_IN_PROGRESS`,
`IMPORT_ROLLBACK_FAILED`) that real CloudFormation accepts but Overcast never
emits. Validating against only the produced subset would have rejected a
value AWS itself allows — the wrong-answer direction, in miniature. The fix
adds the three missing constants and validates
`StackStatusFilter` against the complete 23-value enum, so a caller filtering
on an import state Overcast doesn't emit is still accepted (and, correctly,
matches no stacks), while a genuinely bogus value gets AWS's
`ValidationError`.

### 7. Everything else — no filter parameter

`rds`, `elbv2`, `ecs`, `eks`, `dynamodb`, `efs`, `elasticache` and the rest
select by identifier, by prefix, or not at all. `lambda`'s `FilterCriteria` and
`pipes`' filter patterns are **event-source filtering** — a different thing
entirely, matching records rather than selecting resources, with its own
AWS-defined pattern language that is already implemented. `cloudformation`'s
`TagFilters` are S3 lifecycle-rule properties being translated, not a query API.

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
3. ~~**The three that matched nothing**~~ **Done, under #1188.**
   `secretsmanager:ListSecrets`/`BatchGetSecretValue` now refuse an
   unrecognised `Filter.Key` with `InvalidParameterException`;
   `stepfunctions:ListExecutions` now refuses an unrecognised `statusFilter`
   with `ValidationException`; `cloudformation:ListStacks` now refuses an
   unrecognised `StackStatusFilter` value with `ValidationError`, validated
   against the complete AWS `StackStatus` enum rather than the subset Overcast
   produces. None of the three had a wrong answer to prevent — an empty page
   was always a defensible reading of "nothing matched that" — so this was a
   fidelity improvement, not a bug fix in the #1032 sense.

   Note the asymmetry the corrected sweep made visible, and that #1188 then
   closed: **every** divergence this survey found was in the fail-safe
   direction, and now every one of them refuses instead. The wrong-answer
   direction — ignore the filter, return everything — has never existed
   anywhere in Overcast since #1032/#1041, and the fail-safe direction — match
   nothing rather than refuse — no longer exists in Overcast either.

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
