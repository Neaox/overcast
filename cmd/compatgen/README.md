# compatgen

`compatgen` turns the pruned AWS shape snapshot (`models/aws/shapes/`) plus
the hand-curated recipes under `compat/model/recipes/` into the compat
scenario IR (`compat/model/scenarios/`), the refusal report
(`compat/model/gaps.json`), the generated registry sibling
(`compat/suites/registry.generated.json`) and — for the suites whose SDK has
no dynamic-dispatch API — the source they compile
(`compat/suites/go-sdk/internal/groups/scenarios_*_gen.go`). It is a
build-time tool whose output is committed data; nothing under `compat/`
imports it or any other emulator Go code.

The IR, the recipe format and the refusal vocabulary are documented in
[compat/model/README.md](../../compat/model/README.md). The design is
[docs/plans/compat-coverage-modelgen.md](../../docs/plans/compat-coverage-modelgen.md) §3.

## Usage

```sh
make generate-compat-model        # regenerate scenarios, gaps.json and the registry
make compat-model-check           # prove the committed output is byte-identical (CI)
```

The underlying command is `go run -tags dev ./cmd/compatgen`; the `dev` tag
is required because the capability table it reads
(`internal/capabilities/all.gen.go`) is dev-only, exactly as for `cmd/capgen`.

| Flag | Meaning |
| --- | --- |
| *(none)* | generate every recipe under `compat/model/recipes/` and rewrite every output |
| `-check` | regenerate in memory and compare byte-for-byte, writing nothing; also fails on a scenario file whose recipe is gone |
| `-scaffold <service>` | print a recipe skeleton for a service in the shape snapshot |
| `-review-report [service]` | print the Markdown review report for a PR body |
| `-explain <group>/<test> -lang <python\|node\|cli\|go\|java\|dotnet\|rust>` | render one generated test as pseudo-code |
| `-sample <n>` | scenarios rendered in the review report (default 3, fixed seed) |
| `-root <dir>` | repository root (default `.`) |

## Inputs

- `models/aws/shapes/<service>.json` — the pruned shape snapshot written by
  `cmd/awsmodelgen`. A recipe for a service the snapshot does not cover is
  refused with the instruction to add it to `models/aws/shapes-services.txt`
  and run `make generate-aws-operations`. The generator never reads the raw
  Smithy corpus, and never runs at test time.
- `compat/model/recipes/<service>.json` — validated against
  `recipe.schema.json` on load, then against the model: every operation must
  exist, every path must resolve, every literal must be of the member's
  kind and inside its constraints.
- `compat/model/values.json` — curated literals, validated the same way.
- `compat/model/promotions.json` — the candidate → gated soak ledger, and
  the one thing that can move a generated group's `state`. It is written only
  by `go run ./cmd/compat --promote-generated`, so the generator keeps sole
  ownership of `registry.generated.json`; an entry for a group no scenario
  produces is refused. Missing file, or a group with no entry: `candidate`.
- `internal/capabilities/all.gen.go` — which operations the emulator
  implements. An operation with a status other than `Unsupported` is
  implemented and may never sit in a probe group.
- `internal/awsapi` — the routing manifest, for the scenario file's `client`
  header (SDK id, protocol, API version, target prefix). It is *not* what
  ties a recipe to a snapshot: a recipe whose Overcast key differs from the
  model's says so itself, in its `model` field (`"service": "cognito"`,
  `"model": "cognito-identity-provider"`), and that is the name the snapshot
  file carries. The manifest's alias table is used only by `-scaffold`, to
  print the Overcast key for a model service.

## Adding a service

1. Make sure its shapes are in the snapshot (`models/aws/shapes-services.txt`).
2. `go run -tags dev ./cmd/compatgen -scaffold <service> > compat/model/recipes/<service>.json`
   and complete the skeleton against the real AWS API semantics. It marks its
   own three kinds of line and the schema refuses each of them, so it cannot
   be mistaken for a finished recipe: `$comment` on a derived value names the
   trait or rule that produced it, `$todo` stands where only a human can
   supply the value, and `$review` marks a lifecycle whose create or delete is
   not read-only-safe by the verb rule. See
   [compat/model/README.md § Scaffolding](../../compat/model/README.md#scaffolding).
3. `make generate-compat-model`, then read `gaps.json`: each refusal is a
   line in the recipe or in `values.json`, or a deliberate gap.
4. Put `go run -tags dev ./cmd/compatgen -review-report <service>` in the PR
   body. It lists operations covered vs modeled, every refusal with its
   reason, every automatic name-match binding (the riskiest inference the
   generator makes), every curated or synthetic value it bound, and a fixed
   sample of scenarios rendered as pseudo-code.

One service per PR: the reviewable artifacts are the recipe, the values
entries and the gap report.

## What the generator will not do

- Guess a value. A required member no rule can bind refuses the operation
  (`unbound-required-member:<Member>`). Rule 4 derives a literal only where
  the model's constraints enumerate or bound the legal values: the first
  member of an enum (the snapshot's own order where it survives the snapshot —
  see `compat/model/README.md`), a range minimum, and
  `false` for a required boolean — two legal values, of which `false` is the
  one asking the service to do less. §3.3's fourth candidate,
  "the shortest legal string for a pattern", is deliberately **not**
  implemented: a pattern constrains a string's syntax, never its reference,
  so the shortest match for `^arn:aws:.*` is a well-formed ARN of something
  that does not exist. The emulator accepts far more of those than AWS does,
  which is the class of value §3.10 says belongs in the gap report.
- Point a probe at anything the run owns. A probe is the one generated call
  no create/delete pair contains, so rules 1 and 2 are off inside a probe
  group: it binds only curated or synthetic literals. A member only a live
  export could supply refuses the operation
  (`probe-binds-live-resource:<Member>`), and an operation a recipe lists
  under `neverProbe` — irreversible even with a stranger's identifiers, or
  taking no identifier that could miss — is refused before it is bound
  (`never-probe`).
- Emit a test without an assertion. The only test constructor takes its first
  assertion as a non-optional argument, the schema says `minItems: 1`, and
  `validateScenario` re-checks the finished file.
- Probe an implemented operation, or generate an update without a declared
  mutation, or a create/delete with no read-back path. Authored coverage is
  held to the same rule rather than exempted from it: only a clause that
  calls the service again — a `readback`, a `listContains` or `absent` with
  its own call, or an `eventually` around one — counts as verifying anything,
  so an authored `create.assert` made only of `responseField` clauses is
  refused (`no-readback-path`), and an authored update-family operation whose
  clauses all read its own response is refused (`update-without-readback`).
- Assert something the call cannot have changed. A probe of an operation
  that returns nothing is refused (`no-output-to-assert`) rather than given a
  read-back of the resource it names, which would hold whether or not the
  call did anything.
- Assert a pagination token. `identityMember` skips the member `@paginated`
  names as its `outputToken` and every member named like one (`NextToken`,
  `Marker`, anything ending in `Token` or `Marker`): that field is precisely
  what AWS omits on a single-page answer, so asserting it non-empty asserts
  the opposite of a correct response. A `List*` left with only its page gets
  `{"isList": true}` on that list instead — true of an empty or omitted page
  (some services omit the member instead of serializing `[]`), false of a
  present response that is not a list — and one with neither an identity nor
  a single list is refused (`no-output-to-assert`).
- Emit a timestamp, blob or document literal: the SDKs disagree on how those
  are passed and an interpreter has no model to convert with, so such a
  member stays unbound and the operation is refused.
- Write a group to the registry while no suite has a scenario backend. The
  `scenarioBackends` table in `registry.go` is that availability. It was
  empty until the G2 interpreters landed, so the registry stayed `groups: []`
  and the empty-file gate kept holding; the scenario files and `gaps.json`
  are fully generated regardless.
- List a suite against a group its backend cannot execute. `suites` is
  derived from backend availability, so a group the Go emitter refused is
  scoped to the other backends instead — and a group no backend can run is
  left out of the registry altogether. Listing it anyway would turn the
  refusal into a hard failure in that suite, whose loader treats a generated
  test with no backend as a coverage hole rather than a skip.

## Source emitters

The three interpreter suites execute the IR at run time. The typed SDKs
cannot: they have no public dynamic-dispatch API, and
[the plan](../../docs/plans/compat-coverage-modelgen.md) §3.2 rejects reaching
into their marshaller layers to fake one, because the reason for running eight
suites is that each exercises its own real typed serialization path. So
`emit_go.go` writes Go instead — one function per scenario test, each building
a real `*sqs.CreateQueueInput` and calling a real client method — which the
`go-sdk` suite's ordinary build compiles.

What is emitted is the *data* plus the typed calls. The semantics — the
context bag, `$name`/`$ref`, the closed check set, error matching,
`eventually`, the six-field failure message — are written once by hand in
`compat/suites/go-sdk/internal/scenario` and never re-emitted.

### Where the types come from

A member's Go spelling depends on what smithy-go made of it, and the pinned
shape snapshot cannot say: the snapshot and the vendored SDK are generated from
different revisions of the same AWS model, and for the pilot service they
already disagree — `ReceiveMessage`'s `MaxNumberOfMessages`,
`VisibilityTimeout` and `WaitTimeSeconds` target `NullableInteger` in
`models/aws/shapes/sqs.json`, which says pointer, and are plain `int32` fields
in `aws-sdk-go-v2/service/sqs`.

So `gosdktypes.go` asks the SDK, with `golang.org/x/tools/go/packages`, loading
`github.com/aws/aws-sdk-go-v2/service/<pkg>` from **the `go-sdk` suite's own
module** — the module the emitted source is compiled in, so the answer is the
one the compiler will give. `emit_go_spell.go` turns each `<Op>Input` field's
declared type into source:

| field type | value | emitted |
| --- | --- | --- |
| `*string` | `"blue"` | `aws.String("blue")` |
| `int32` | `30` | `30` |
| `types.PolicyType` | `"SCP"` | `types.PolicyType("SCP")` |
| `map[string]string` | `{"a":"b"}` | `map[string]string{"a": "b"}` |
| `[]types.Tag` | `[{"Key":"k"}]` | `[]types.Tag{{Key: aws.String("k")}}` |
| `*string` | `{"$ref":"q"}` | `aws.String(scenario.Bind[string](b, "M", scenario.Ref("q")))` |

Only the last row leaves anything to run time, and only because a `$ref` cannot
be known before the run: `scenario.Bind` converts the evaluated value to the
one scalar type the field wants. Nothing reflects.

Two things keep the emitter honest:

- **One naming table.** Everything it knows about spelling Go is in
  `emit_go.go`'s `goName*` functions and `emit_go_spell.go`'s `goSpeller`, and
  `-explain -lang go` renders through the same `goInputLines`, so the
  pseudo-code a reader reproduces a failure with is the source the emitter
  wrote — pointers and all. `TestExplainGoRendersTheEmittedCall` asserts it.
- **It refuses rather than guesses.** Five things produce
  `go-emit-unsupported:<Member>` in `gaps.json`, and the group is then scoped
  away from `go-sdk` rather than emitted as a guess or silently dropped:

  | refusal | what it means |
  | --- | --- |
  | the modeled kind has no IR literal | a timestamp, blob, document or union |
  | `<Op>Input` is not declared | the vendored SDK is older than the pinned model |
  | the SDK has no field for the member | smithy-go renamed or dropped it |
  | the field's type has no Go literal | a union, or a type from a third package |
  | a value-typed member is set to its zero value | the SDK would not serialize it (`compat/model/README.md` § Values) |

  Nothing in the pilot corpus reaches any of them.

The emitted bytes go through `go/format` before they are written, and
generation fails if they will not parse. A golden file under
`testdata/golden/` holds the emitted source for the fixture service, so what
the emitter writes is reviewed as a diff rather than inferred from the
generator's code.

## Determinism

Regeneration is byte-identical: sorted keys, struct fields in declaration
order, two-space indentation, no HTML escaping, LF line endings, one trailing
newline. `TestCommittedCorpus_isInSyncWithTheGenerator` proves the committed
corpus is what the generator produces (the offline analogue of the shape
snapshot's `shapes-sha256` gate), and `make compat-model-check` runs the same
check from the command line. CI runs both.

## Tests

`go test -tags dev ./cmd/compatgen` runs unit tests over a fixture service
under `testdata/` (shapes, recipe and values). The emitted Go is proved to
parse and to be gofmt-clean here, while the proof that it *compiles* is the
`go-sdk` suite's own build.

**Which tests read which SDK.** The emitter needs real Go types, so
`testdata/awssdk` is a checked-in stand-in for the AWS SDK for Go v2: a module
of its own, under the SDK's own module path, declaring the fixture service's
input structs and nothing else. Every test of the emitter — the golden file,
the spelling table, each refusal, the `-explain` agreement — resolves against
it, so it type-checks real Go with no module cache and no network.

The four tests that regenerate the *committed* corpus need the real vendored
SDK instead, out of `compat/suites/go-sdk/go.mod`. They skip when its
dependencies cannot be resolved, which keeps `go test` runnable offline; the
unconditional gate is `make compat-model-check`, whose second command is
`go run -tags dev ./cmd/compatgen -check` and which fails outright. That is
also the only place a real fetch can happen — the first run in a fresh
environment downloads what type-checking the two pilot services needs, and the
module cache serves every run after it.

`OVERCAST_UPDATE_GOLDEN=1 go test -tags dev -run TestEmitGo ./cmd/compatgen`
rewrites `testdata/golden/scenarios_widgets_gen.go.golden`. Read the diff
before committing it — the golden file is the review artifact for what the
emitter writes, and one regenerated without being read proves nothing. Its five resources between
them carry every recipe role — a full lifecycle, a pre-existing resource, a
setup-only resource whose create cannot be bound and one that requires it,
authored operations, an authored create assertion, an async budget, and both
tag shapes — so the suite exercises every binding rule, every refusal reason,
the assertion contract, determinism, scaffolding, explain rendering and the
review report, plus the sync and schema checks over the committed corpus.
