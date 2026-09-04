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
| `scenarios/<service>.json` | `cmd/compatgen` | the scenario IR, one file per service (`scenario.schema.json`) |
| `gaps.json` | `cmd/compatgen` | every operation the generator refused, with a reason (`gaps.schema.json`) |
| `../suites/registry.generated.json` | `cmd/compatgen` | the generated registry sibling every loader concatenates |

`<service>` is the Overcast capability key, exactly as a registry group's
`service` field spells it. Generated files are rewritten wholly on every run;
never edit them.

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
generator refuses one), so every other clause makes a call of its own.

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
| node-js-sdk | `@aws-sdk/client-<kebab(sdkId)>`, class `<PascalCase(sdkId)>Client` | `new <Op>Command(params)` |
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
| `create` | the create call. Omit it for a **pre-existing** resource (an organization): then `read` is the group's only derived test and its `exports` are what other resources bind to |
| `exports` | export name → path in the create response |
| `derived` | exports that need a second call (an ARN only `GetQueueAttributes` returns); each becomes a read-back in the create test |
| `binds` | input member name → context path, for every operation the resource takes part in — binding rule 1 |
| `read` | the read-back: `identityPath` must equal the export `identity` names (or, without `identity`, match the model's pattern). `consuming: true` marks a read that changes state (`ReceiveMessage`); it is emitted once, as its own test, and never used as a read-back. `exports` take values from the read response |
| `reads` | further reads, each its own test |
| `list` | the list-membership check: an item at `itemsPath` whose `identityPath` equals the `identity` export. Its own test runs last before delete |
| `mutable` | one entry per update: `member` (dotted input path) is set to `to`, then `read` must show `readPath == to`. `from` is merged into the create params, so the update is a real change and the create's read-back asserts the initial value |
| `tags` | tag/untag/list operations; the generator reads whether the service carries tags as a string map or a list of `{Key, Value}` from the model and emits tag → list → untag tests with the literal `compat=scenario` |
| `delete` | the delete call; absence is proven by `notFound` (the read must fail with that error) or, failing that, by `list` non-membership |
| `notFound` | the modeled error shape the read raises after delete |
| `async` | wraps every read-back, list-membership and absence clause in `eventually` |
| `operations` | authored coverage for operations outside the lifecycle vocabulary, written in the IR's own assertion vocabulary; `name` gives a variant test name when the operation already has a test |

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
| `update-without-mutable` | an implemented Update/Set/Tag/Untag operation with no `mutable` or `tags` entry |
| `no-readback-path` | the role exists but nothing can verify it: a create with no read, list or authored assertion; a delete with neither `notFound` nor `list`; a mutation whose read consumes |
| `probe-of-implemented-op` | an implemented operation the recipe gives no role — it may not be probed, so it needs a role |
| `no-output-to-assert` | a probe of an operation that returns nothing and is bound to no readable resource |
| `setup-refused:<resource>` | a required resource could not be bound |
| `unsupported-tag-shape:<Shape>` | the tag member is neither a string map nor a list of `{Key, Value}` |

Refusals are a feature. Fixing one is a line in a recipe or in
`values.json`; guessing is never an option.
