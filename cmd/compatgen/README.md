# compatgen

`compatgen` turns the pruned AWS shape snapshot (`models/aws/shapes/`) plus
the hand-curated recipes under `compat/model/recipes/` into the compat
scenario IR (`compat/model/scenarios/`), the refusal report
(`compat/model/gaps.json`) and the generated registry sibling
(`compat/suites/registry.generated.json`). It is a build-time tool whose
output is committed data; nothing under `compat/` imports it or any other
emulator Go code.

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
| *(none)* | generate every recipe under `compat/model/recipes/` and rewrite all three outputs |
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
- `internal/capabilities/all.gen.go` — which operations the emulator
  implements. An operation with a status other than `Unsupported` is
  implemented and may never sit in a probe group.
- `internal/awsapi` — the routing manifest, for the scenario file's `client`
  header (SDK id, protocol, API version, target prefix), and the alias table
  that maps a model service to its Overcast key.

## Adding a service

1. Make sure its shapes are in the snapshot (`models/aws/shapes-services.txt`).
2. `go run -tags dev ./cmd/compatgen -scaffold <service> > compat/model/recipes/<service>.json`
   and complete the skeleton against the real AWS API semantics — the
   skeleton carries `$todo` placeholders the schema rejects, so it cannot be
   mistaken for a finished recipe.
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
  (`unbound-required-member:<Member>`).
- Emit a test without an assertion. The only test constructor takes its first
  assertion as a non-optional argument, the schema says `minItems: 1`, and
  `validateScenario` re-checks the finished file.
- Probe an implemented operation, or generate an update without a declared
  mutation, or a create/delete with no read-back path.
- Emit a timestamp, blob or document literal: the SDKs disagree on how those
  are passed and an interpreter has no model to convert with, so such a
  member stays unbound and the operation is refused.
- Write a group to the registry while no suite has a scenario backend. The
  `scenarioBackends` table in `registry.go` is that availability; it is
  empty until the G2 interpreters land, so the registry stays `groups: []`
  and the empty-file gate keeps holding. The scenario files and `gaps.json`
  are fully generated regardless.

## Determinism

Regeneration is byte-identical: sorted keys, struct fields in declaration
order, two-space indentation, no HTML escaping, LF line endings, one trailing
newline. `TestCommittedCorpus_isInSyncWithTheGenerator` proves the committed
corpus is what the generator produces (the offline analogue of the shape
snapshot's `shapes-sha256` gate), and `make compat-model-check` runs the same
check from the command line. CI runs both.

## Tests

`go test -tags dev ./cmd/compatgen` runs unit tests over a fixture service
under `testdata/` (shapes, recipe and values) that exercises every binding
rule, every refusal reason, the assertion contract, determinism, scaffolding,
explain rendering and the review report, plus the sync and schema checks over
the committed corpus.
