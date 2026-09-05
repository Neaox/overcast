# The compat scenario model

This directory is the model-driven half of the compat suite: the inputs a
human curates, the scenario IR `cmd/compatgen` generates from them, and the
schemas both are held to. Interpreters (`python-sdk`, `node-js-sdk`, `cli`)
execute the scenario files; typed-SDK suites compile them to source. This
page is the normative description of the IR — an interpreter is written from
it, and where it and `scenario.schema.json` disagree, that is a bug in one of
them.

Design: [docs/plans/compat-coverage-modelgen.md](../../docs/plans/compat-coverage-modelgen.md) §3.
Generator: [cmd/compatgen/README.md](../../cmd/compatgen/README.md).

## Layout

| Path | Who writes it | What it is |
| --- | --- | --- |
| `recipes/<service>.json` | a human | one recipe per service — the curated layer (`recipe.schema.json`) |
| `values.json` | a human | curated literals for required members no recipe binds (`values.schema.json`) |
| `promotions.json` | `cmd/compat --promote-generated` | the candidate → gated soak ledger, read by the generator to emit each group's `state` (`promotions.schema.json`) |
| `scenarios/<service>.json` | `cmd/compatgen` | the scenario IR, one file per service (`scenario.schema.json`) |
| `gaps.json` | `cmd/compatgen` | every operation the generator refused, with a reason (`gaps.schema.json`) |
| `../suites/registry.generated.json` | `cmd/compatgen` | the generated registry sibling every loader concatenates |

`<service>` is the Overcast capability key, exactly as a registry group's
`service` field spells it. Generated files are rewritten wholly on every run;
never edit them.

`promotions.json` is an **input**, not generator output — which is the point.
A generated group's `state` has to move from `candidate` to `gated` once the
nightly soak has watched it agree with itself, and letting that soak rewrite
the field in `registry.generated.json` would put two tools in charge of one
generated file. So the soak writes the ledger, the generator reads it, and
`-check` stays byte-identical across a promotion: regeneration is still a pure
function of committed inputs. Do not hand-edit it to gate a group — the entry
is the evidence of N agreeing runs, and typing one is the hand edit the soak
exists to replace. See
[docs/plans/compat-coverage-modelgen.md](../../docs/plans/compat-coverage-modelgen.md) § 3.6.

## The scenario file

```jsonc
{
  "version": 1,
  "service": "sqs",
  "client": { "sdkId": "SQS", "endpointPrefix": "sqs", "signingName": "sqs",
              "protocol": "awsJson1_0", "apiVersion": "2012-11-05", "targetPrefix": "AmazonSQS" },
  "groups": [
    {
      "name": "sqs-gen-queue",
      "kind": "lifecycle",
      "setup":    [ <call>, ... ],
      "tests":    [ <test>, ... ],
      "teardown": [ <call>, ... ]
    }
  ]
}
```

A **group** is a registry group. Its name is `<service>-gen-<resource>` for a
lifecycle group and `<service>-gen-probe` for the probe group. Every group is
independently runnable: `setup` creates what it needs, `teardown` removes it,
and nothing crosses group boundaries.

An interpreter runs one group as:

1. Create an empty **context** — a map from context path to value.
2. Run every `setup` call in order. A failure (an error, or an unresolvable
   `$ref`) reports every test in the group as `skip` with the reason
   `setup failed: <error>`, then still runs teardown.
3. Run every test in order (the registry's `depends` gives the loader the
   same order and the usual dependency skip).
4. Run every `teardown` call in order, each one individually wrapped: an
   error or an unresolvable `$ref` skips that call and continues with the
   next.

### `client`

What is needed to construct a client without a naming table of the
interpreter's own (§7.3 of the plan). `protocol` is the Smithy protocol trait
name (`awsJson1_0`, `awsJson1_1`, `awsQuery`, `ec2Query`, `restJson1`,
`restXml`, `rpcv2Cbor`, `rpcv2Json`); `targetPrefix` is present for the two
AWS JSON protocols, and the `X-Amz-Target` header is `<targetPrefix>.<Op>`.
Per-SDK package names are deliberately not in the file — see
[Naming](#naming).

### A call

```jsonc
{ "op": "CreateQueue",
  "params": { "QueueName": { "$name": "q" }, "Attributes": { "VisibilityTimeout": "30" } },
  "export": { "queue.url": "$.QueueUrl" } }
```

`op` is the AWS operation name; `params` is the input, member by member,
each a [value](#values); `export` (optional) sets context paths from the
response. An export whose path does not resolve in the response is an error
for the step that carries it.

### A test

```jsonc
{ "name": "SetQueueAttributes",
  "op": "SetQueueAttributes",
  "call": <call>,
  "assert": [ <assertion>, ... ],
  "depends": ["CreateQueue"] }
```

`name` is the registry test name and `op` the operation it exercises (they
differ only for variants: `SendMessageBeforePurge` has `op: SendMessage`).
`assert` always has at least one clause — the schema says `minItems: 1`, and
the generator's only test constructor takes the first clause as a
non-optional argument, so a test with nothing to assert cannot be written.
`depends` names the earlier tests in the group whose exports this test
consumes; it is mirrored into the registry, where the loaders already honour
it.

A test passes when its call succeeds and every clause holds, in order. The
primary call's response is what `responseField` and call-less `listContains`
clauses look at. If a clause of kind `errorCode` is present, the primary call
is *expected* to fail: catch the error and check its code. Such a test
carries no `export` and no clause that reads the primary response (the
generator refuses one), so every other clause makes a call of its own. No
derived path emits `errorCode` yet — the negative-path variants of §3.4 are
authored, in an `operations` entry — but the kind is part of the IR and every
interpreter implements it.

### Assertions

The set is closed. `kind` selects the fields.

| Kind | Fields | Holds when |
| --- | --- | --- |
| `responseField` | `checks` | every check holds against the test's own response |
| `readback` | `call`, `checks` | `call` succeeds and every check holds against *its* response; the call's `export`s are applied |
| `listContains` | `itemsPath`, `where`, optional `call` | the list at `itemsPath` (of `call`'s response, else the test's own) is non-empty and contains an item matching every `where` entry |
| `absent` (list form) | `itemsPath`, `where`, optional `call` | no item of the list at `itemsPath` matches every `where` entry; a missing list counts as empty |
| `absent` (error form) | `call`, `error` | `call` fails with `error` |
| `errorCode` | `error` | the test's own call fails with `error` |
| `eventually` | `maxAttempts`, `delayMs`, `assert` | the inner clause (`readback`, `listContains` or `absent`) holds on some attempt: evaluate it, and on failure wait `delayMs` and try again, at most `maxAttempts` times in all. Exports from a `readback` inside are applied when the attempt passes |

**`checks`** maps a [path](#paths) to exactly one of:

| Check | Holds when |
| --- | --- |
| `{"nonEmpty": true}` | the path resolves to a value that is not `null`, `""`, `[]` or `{}`; numbers and booleans are never empty |
| `{"isList": true}` | the path resolves to a list, **empty or not** — or does not resolve at all. It exists because `nonEmpty` cannot say "this is a page of results": a single-page `List*` legally returns an empty page, and several AWS services (SQS's `ListQueues` among them) omit that member entirely rather than serialize `[]`, so a check that failed on absence would fail against real AWS. Absence is accepted for the same reason a missing list already counts as empty for `absent` and `listContains`; a present value that is not a list still fails the check |
| `{"equals": <value>}` | the path resolves and the value is equal, as JSON, to the evaluated expression |
| `{"matches": "<regex>"}` | the path resolves to a string matching the regular expression (RE2-compatible syntax; anchored only where the pattern anchors itself) |
| `{"missing": true}` | the path does not resolve — any segment absent |

**`where`** maps an item-relative path to a value; `$` is the item itself
(`{"$": {"$ref": "queue.url"}}` for a list of strings). An item matches when
every entry is equal, as JSON.

**`error`** carries the modeled `shape` name and the wire `code` (the
`awsQueryError` code where the service declares one, else the shape name
again — for SQS, `QueueDoesNotExist` and
`AWS.SimpleQueueService.NonExistentQueue`). SDKs disagree on which of the two
they surface, so an interpreter accepts an error whose reported code **or**
type name equals **either**. An error whose code matches neither, or a call
that succeeds, fails the clause.

Equality "as JSON": compare the SDK's value after mapping it to JSON the way
the SDK itself would (a boto3 `int` is a JSON number, a `bool` a boolean, a
`dict` an object); the generator only ever emits an `equals` literal of the
member's modeled kind, so no coercion is needed. Timestamps and blobs are
never compared.

### Paths

`$` is the response; `.Name` selects a structure member or map key; `[n]`
selects a list element (zero-based). `$.Attributes.QueueArn`,
`$.Messages[0].ReceiptHandle`, `$.Tags.compat`. Nothing else — no wildcards,
filters, quoting or recursive descent. Member names are the modeled names,
which is what every SDK and the CLI's JSON output use.

### Values

A value is JSON. An object with exactly one `$`-prefixed key is an
expression; any other object is a structure or map whose values are values;
an array is a list of values; a scalar is itself.

| Expression | Evaluates to |
| --- | --- |
| `{"$lit": <json>}` | the JSON, verbatim, never interpreted (use it for an object whose keys start with `$`) |
| `{"$ref": "queue.url"}` | the context value at that path; unresolvable is an error for the step |
| `{"$name": "q"}` | the string `{runId}-{group}-q`, where `runId` is the suite's run id (`OVERCAST_COMPAT_RUN_ID`) and `group` the group name — deterministic within a run, so the same suffix names the same resource wherever it appears in the group |
| `{"$concat": [<part>, ...]}` | the parts joined; a part that is a bare string is a literal, otherwise it is an expression that must evaluate to a string |
| `{"$index": [<value>, n]}` | element `n` of a list-valued expression |

No conditionals, no arithmetic, no scripting: eight implementations have to
agree on every value.

`$name` is the only way a generated test names a resource, which is what
makes the name-hygiene convention (`{runId}-<group-token>-…`) hold by
construction: the group token is the whole group name.

### Failure messages

Every interpreter failure message must carry, in this order:

1. `group/test` — `sqs-gen-queue/SetQueueAttributes`
2. the operation — of the primary call, or of the clause's own call for
   `readback`, `listContains` and `absent`
3. the **exact params JSON sent**, after evaluating every expression
4. the assertion kind and, for `checks`/`where`, the path
5. expected vs actual — the check, and the value the path resolved to (or
   `<missing>`); for an error clause, the accepted codes and the error
   actually raised (or `<no error>`)
6. the scenario file and the step index — `compat/model/scenarios/sqs.json`
   `assert[2]` (or `setup[1]`, `call`, `teardown[0]`)

`go run -tags dev ./cmd/compatgen -explain sqs-gen-queue/SetQueueAttributes -lang python`
renders the same test as pseudo-code so a failure can be reproduced by hand.

### Naming

The IR carries `sdkId`, `endpointPrefix` and `signingName` and nothing
SDK-specific. Each backend derives what it needs:

| Backend | Client | Operation |
| --- | --- | --- |
| python-sdk | `boto3.client(<endpointPrefix>)` — botocore's service name is the endpoint prefix for both pilot services | `getattr(client, xform_name(op))(**params)` |
| node-js-sdk | `@aws-sdk/client-<lower(sdkId), whitespace/underscore to "-">` (so DynamoDB → dynamodb, not dynamo-db), class `<sdkId's alphanumerics>Client` | `new <Op>Command(params)` |
| cli | `aws <endpointPrefix> <kebab(op)> --cli-input-json '<params>'` | same |
| go-sdk | `github.com/aws/aws-sdk-go-v2/service/<lower(sdkId, spaces removed)>` | `client.<Op>(ctx, &<Op>Input{…})` |
| java-sdk | `<PascalCase(sdkId)>Client` | `client.<lowerFirst(op)>(<Op>Request.builder()…)` |
| dotnet-sdk | `Amazon<PascalCase(sdkId)>Client` | `client.<Op>Async(new <Op>Request {…})` |
| rust-sdk | `aws_sdk_<snake(sdkId)>` | `client.<snake(op)>()…send()` |

Where a derivation is known to break the interpreter needs a small override
table of its own, and the plan asks for those to be recorded as follow-ups
rather than smuggled into the IR. None breaks for `sqs` or `organizations`.
Known cases elsewhere: botocore's service name differs from the endpoint
prefix for a handful of services (`elasticloadbalancing` → `elb`, `monitoring`
→ `cloudwatch`, `email` → `ses`, `states` → `stepfunctions`); the Go SDK
package for `Cost Explorer` is `costexplorer` (derivable) but for `SFN` it is
`sfn` and for `ELB` it is `elasticloadbalancing` (not derivable from the SDK
id).

## Recipes

A recipe says what the model cannot: which operation creates a resource,
which response field identifies it, how it is read back, what to mutate and
where the change shows up, how to delete it and what not-found looks like.
Structure is generated from that; semantics stay curated. Start from
`go run -tags dev ./cmd/compatgen -scaffold <service>` and complete by hand
against the real AWS API.

```jsonc
{
  "service": "sqs",
  "resources": [{
    "id": "queue",                      // context prefix and group name (sqs-gen-queue)
    "requires": ["dlq"],                // created in setup first, deleted in teardown last
    "create":  { "op": "CreateQueue", "params": { "QueueName": { "$name": "q" } } },
    "exports": { "url": "$.QueueUrl" }, // from the create response
    "derived": [{ "export": "arn", "op": "GetQueueAttributes",
                  "params": { "QueueUrl": { "$ref": "queue.url" }, "AttributeNames": ["QueueArn"] },
                  "path": "$.Attributes.QueueArn" }],
    "binds":   { "QueueUrl": "queue.url", "QueueArn": "queue.arn" },
    "read":    { "op": "GetQueueAttributes", "params": { "AttributeNames": ["All"] },
                 "identityPath": "$.Attributes.QueueArn", "identity": "arn" },
    "reads":   [{ "op": "GetQueueUrl", "params": { "QueueName": { "$name": "q" } },
                  "identityPath": "$.QueueUrl", "identity": "url" }],
    "list":    { "op": "ListQueues", "params": { "QueueNamePrefix": { "$name": "q" } },
                 "itemsPath": "$.QueueUrls", "identityPath": "$", "identity": "url" },
    "mutable": [{ "op": "SetQueueAttributes", "member": "Attributes.VisibilityTimeout",
                  "from": "30", "to": "60", "readPath": "$.Attributes.VisibilityTimeout" }],
    "tags":    { "tag": { "op": "TagQueue", "member": "Tags" },
                 "untag": { "op": "UntagQueue", "member": "TagKeys" },
                 "list": { "op": "ListQueueTags", "path": "$.Tags" } },
    "delete":  { "op": "DeleteQueue" },
    "notFound": { "error": "QueueDoesNotExist" },
    "async":   { "maxAttempts": 10, "delayMs": 500 },
    "operations": [ { "op": "PurgeQueue", "assert": [ <assertion>, ... ] } ]
  }]
}
```

| Field | Meaning |
| --- | --- |
| `id` | the resource's name in context paths and its group |
| `setupOnly` | exists only to be required by others; no group of its own |
| `requires` | resources created in setup before this one (topological), deleted in teardown after it |
| `create` | the create call. Omit it for a **pre-existing** resource (an organization): nothing creates or deletes it, its `exports` come from its `read`, and that read is emitted as a test rather than as setup. Everything else a recipe can declare still applies — further `reads`, `mutable`, `tags` and authored `operations` all get their tests, so such a group is not the read alone |
| `create.assert` | authored clauses that verify the create in place of the derived read-back and list-membership, for a resource whose read cannot simply be replayed. At least one of them must call the service again, or the create is refused (`no-readback-path`): restating the create's own response is not a read-back |
| `exports` | export name → path in the create response |
| `derived` | exports that need a second call (an ARN only `GetQueueAttributes` returns); each becomes a read-back in the create test |
| `binds` | input member name → context path, for every operation the resource takes part in — binding rule 1 |
| `read` | the read-back: `identityPath` must equal the export `identity` names (or, without `identity`, match the model's pattern). `consuming: true` marks a read that changes state (`ReceiveMessage`); it is emitted once, as its own test, and never used as a read-back. `exports` take values from the read response |
| `reads` | further reads, each its own test |
| `list` | the list-membership check: an item at `itemsPath` whose `identityPath` equals the `identity` export. Its own test runs last before delete; `exports` taken from that response are therefore the freshest values the delete can carry (SQS wants a delete to quote the most recent receipt handle) |
| `mutable` | one entry per update: `member` (dotted input path) is set to `to`, then `read` must show `readPath == to`. `from` is merged into the create params, so the update is a real change and the create's read-back asserts the initial value |
| `tags` | tag/untag/list operations; the generator reads whether the service carries tags as a string map or a list of `{Key, Value}` from the model and emits tag → list → untag tests with the literal `compat=scenario` |
| `delete` | the delete call; absence is proven by `notFound` (the read must fail with that error) or, failing that, by `list` non-membership |
| `notFound` | the modeled error shape the read raises after delete |
| `async` | declares the resource eventually consistent: every clause that verifies by calling the service again is wrapped in `eventually` — the derived read-back, list-membership and absence clauses, and authored clauses too. A clause that only reads the test's own response is left alone (retrying it would re-read one fixed response), and so is an authored `eventually`, whose budget its author already chose |
| `operations` | authored coverage for operations outside the lifecycle vocabulary, written in the IR's own assertion vocabulary; `name` gives a variant test name when the operation already has a test. An update-family operation (`Update*`, `Set*`, `Put*`, `Tag*`, `Untag*`) needs at least one clause that calls the service again, exactly as the derived path needs a `mutable` entry, or it is refused (`update-without-readback`) |

One field sits outside the resource list. **`neverProbe`** maps an operation
the generator may never place in the probe group to the curated reason why,
which is what `gaps.json` then reports. It is the half of the probe-safety
rule the binder cannot see: the binder refuses a probe that would bind a value
exported from a resource the run owns, but an operation that is irreversible
against *anyone's* identifiers — or that takes no identifier that could miss,
like `EnableAllFeatures` or `DeleteOrganization` — has to be named. Each entry
is one sentence saying what the call does that cannot be undone.

```jsonc
{
  "service": "organizations",
  "neverProbe": {
    "CloseAccount": "Closes a member account. AWS suspends it immediately and permanently deletes its resources; the account cannot be reopened."
  },
  "resources": [ ... ]
}
```

Every `params` object lists only what the binder does not supply: the
generator binds each remaining required member by rule (an explicit bind,
an export of the same name — recorded for review —, a curated value, a
constraint-derived scalar) and **refuses** the operation when none applies.
A recipe that contradicts the model — an unknown member, a literal of the
wrong kind, a path that resolves to nothing, a `$name` that cannot fit the
member's length — is an error, not a refusal.

Test order inside a lifecycle group is fixed: create, reads, mutations,
tag/list/untag, authored operations, list, delete. An operation gets at most
one derived test per group; a read or list whose operation already has one
is folded and noted in the review report.

## Refusals

`gaps.json` lists every operation the generator did not produce, keyed by
service and operation, with a stable reason:

| Reason | Meaning |
| --- | --- |
| `unbound-required-member:<Member>` | no rule supplied a legal value; add a `binds` entry or a `values.json` literal |
| `update-without-mutable` | an implemented Update/Set/Put/Tag/Untag operation with no `mutable` or `tags` entry |
| `update-without-readback` | the same operation authored under `operations`, with no clause that calls the service again — it would assert only that the service echoed the request |
| `no-readback-path` | the role exists but nothing can verify it: a create whose read, list and authored `create.assert` between them make no call of their own; a delete with neither `notFound` nor `list`, or one whose `notFound` has no non-consuming read to raise it; a mutation whose read consumes |
| `probe-of-implemented-op` | an implemented operation the recipe gives no role — it may not be probed, so it needs a role |
| `probe-binds-live-resource:<Member>` | a probe would have bound that member to a value exported from a resource the run owns. Add a curated literal to `values.json` — deliberately nonexistent, so the call misses — or leave the operation refused |
| `never-probe` | the recipe's `neverProbe` list forbids probing the operation; the detail is the curated reason |
| `no-output-to-assert` | a probe of an operation that returns nothing a probe can assert: no output at all, or no identity member and no single list to check the shape of. Reading back the resource it names would assert something that was already true before the call, so there is nothing honest to assert |
| `setup-refused:<resource>` | a required resource could not be bound |
| `unsupported-tag-shape:<Shape>` | the tag member is neither a string map nor a list of `{Key, Value}`. `<Shape>` is the bare shape name; the qualified Smithy id is in the detail |

Refusals are a feature. Fixing one is a line in a recipe or in
`values.json`; guessing is never an option.

### What a probe asserts

One clause, on the probe's own response. The generator picks the output's
**identity member** — the first member, in `Arn`, `Id`, `Url`, `Name`,
`Handle`, `Status`, `State` suffix order, that is a scalar; else the first
required member; else the first member at all — and asserts it non-empty.

A **pagination token is never the identity**, and neither is a list. The
token — the member `@paginated` names as its `outputToken`, or any member (or
member target) named `NextToken`, `Marker`, `NextMarker`,
`ContinuationToken`, `NextContinuationToken`, `PaginationToken`, or ending in
`Token` or `Marker` — is exactly the field AWS omits when there is nothing
left to page, so `nonEmpty` on it asserts the opposite of a correct
single-page answer. A list is excluded because a probe populates nothing, so
its length says nothing about the service.

That leaves the `List*` operations, whose only assertable output *is* a list.
They get `{"isList": true}` on the page — the member `@paginated` names as
its `items`, else the output's sole list-typed top-level member — which is
true of a correct empty page, true of an omitted one, and false of a present
response that is not a list at all. Absence is accepted because several AWS
services omit an empty list member instead of serializing `[]` (SQS's
`ListQueues` among them), and a probe run against real AWS must pass there
too. An operation with two lists and no `items` to choose between them, or
with nothing but a token, is refused (`no-output-to-assert`).

### What a probe may bind

A probe is the one generated call no create/delete pair contains: the
emulator does not implement the operation, so nothing undoes it, and the same
scenario file is meant to be runnable against real AWS. Binding rules 1 and 2
are therefore switched off inside a probe group. A probe binds only curated
literals from `values.json` and constraint-derived ones — syntactically valid
and deliberately nonexistent — so the call misses rather than lands. The two
refusals above (`probe-binds-live-resource`, `never-probe`) are the two ways
that rule shows up in the gap report, and a probe group carries no setup and
no teardown because it has nothing to set up.

### What rule 4 will and will not derive

Rule 4 (§3.3) derives a literal only where the model's constraints enumerate
or bound the legal values: the first member of an enum; a range minimum; and
`false` for a required boolean — the shape has exactly
two legal values and `false` is the one that asks the service to do less (no
dry run, no force, no cascade), so the choice is exhaustive rather than a
guess.

"First member" means the order the shape snapshot carries, not sorted order —
but the snapshot cannot always carry one. A `type: enum` shape holds its
members in a JSON object, and `cmd/awsmodelgen` writes object keys through
`encoding/json`, which sorts them, so for those shapes the model's declaration
order is already lost by the time the generator reads it and the pick is the
alphabetically first value. Every enum in the committed snapshots is of that
form today. A `smithy.api#enum` trait is a JSON array and does keep the
model's order, which is the case `EnumValues` preserves; recovering it for the
other form means teaching `cmd/awsmodelgen` to emit an ordered member list.
Either way the pick is deterministic, which is what the byte-identical
regeneration gate needs.

§3.3's fourth candidate, "the shortest legal string for a pattern", is
deliberately not implemented: a pattern constrains a string's *syntax*, never
its *reference*, so the shortest match for `^arn:aws:.*` is a well-formed ARN
of something that does not exist and may not even name the right service. The
emulator accepts far more of those than AWS does, which is exactly the class
of value §3.10 says belongs in the gap report — so the member is refused and
a human writes the literal.
