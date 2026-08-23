# Model-driven compat coverage — scenario generation across every suite

> Status: **in progress** — G0 complete, G1 partly complete; see the § 2
> note for what has landed and what has not. Proposed 2026-08-03. Owner: TBD.
> Siblings written concurrently, and part of the same tier programme:
> [inert-tier-rollout.md](./inert-tier-rollout.md) (Tier 1 implementation — the
> thing generated tests will mostly exercise),
> [services-never-emulated.md](./services-never-emulated.md) (services that stay
> Tier 0 forever), [full-emulation-priority.md](./full-emulation-priority.md)
> (which services earn Tier 2).
> Prior art this plan must slot alongside, not contradict:
> [aws-api-operation-coverage.md](./aws-api-operation-coverage.md) (the pinned
> model snapshot, the generated manifest, the model-refresh workflow) and
> [level2-codegen.md](./level2-codegen.md) Track 3 (server-side model-driven
> generation). Policy this plan is bound by:
> [compat/AGENTS.md](../../compat/AGENTS.md) and
> [compat-baseline-and-uniformity.md](./compat-baseline-and-uniformity.md).

**Tier vocabulary** (shared across the four plans, used throughout):

| Tier | Meaning |
| --- | --- |
| **Tier 0** | No implementation. A protocol-correct `501 NotImplemented` in the right error envelope, guaranteed by [aws-api-operation-coverage.md](./aws-api-operation-coverage.md). |
| **Tier 1 "inert"** | Metadata CRUD only. Correct shapes, status codes, identifiers and error codes; resources are created, described, listed and deleted; CDK/CFN provisioning works; **no behaviour** (a Route 53 zone serves no DNS, a Shield protection protects nothing). |
| **Tier 2** | Full emulation: the resource actually does its job. |

---

## 1. Objective

Every AWS operation known to the emulator's pinned Smithy snapshot that Overcast
either implements or intends to route should have a compat test **in every
suite**, without any of those tests being hand-written eight times.

Concretely: replace linear per-suite authoring with a single declarative
**scenario IR** generated from the pinned AWS models plus a small hand-curated
layer of per-service *recipes*, executed by thin per-suite interpreters (dynamic
SDKs) or compiled from generated source (statically typed SDKs).

Two constraints are non-negotiable and shape every decision below:

1. **Purposeful.** Generated tests satisfy the
   [assertion contract](../../compat/AGENTS.md#assertion-contract) — observable
   state is verified. "The call didn't throw" must be *structurally impossible*
   to emit, not merely discouraged.
2. **Reliable.** The CI gate is zero failures
   ([compat/AGENTS.md § Baseline & uniformity](../../compat/AGENTS.md#baseline--uniformity-policy)),
   and quarantine needs a reviewer's approval. Mass-generating flaky tests would
   turn a working gate into an amnesty queue. Generation must therefore arrive
   behind a mechanical soak, not straight into the gate.

Non-goals: generating *emulator* code (that is
[level2-codegen.md](./level2-codegen.md) Track 3); *synthesizing behavioural
tests from the model* (behaviour cannot be derived from shapes — it is ported to
**hand-authored IR scenarios**, written once and executed by every backend,
§3.11); generating IaC stacks (§3.8); testing services Overcast will never route
at full operation depth (§3.9).

> **Owner decision, 2026-08-03:** the endgame is **IR-first for all compat
> tests**, not "generation as a floor beneath hand-written groups". The existing
> hand-written groups document *what* to test; how it is executed is not
> precious, provided the result is reliable and genuinely exercises the SUT
> through each suite's real SDK path. Per-language test code becomes the
> audited exception (§3.11), because at this volume, manual maintenance that
> scales with coverage will simply be ignored. Sections below that describe
> generated coverage as "additive" describe the **rollout posture**, not the
> steady state.

---

## 2. Current state (verified 2026-08-03)

> **Re-verified 2026-08-23: phase G0 is complete and G1 is partly done.**
> What landed, all under #1113:
>
> | Deliverable | PR |
> | --- | --- |
> | `--shard i/n` over the existing `OVERCAST_COMPAT_GROUPS` plumbing | #1356 |
> | `suites`-scoping amendment (§3.6, §7.2) + its lint, and the `service`-key lint (§7.7) | #1357 |
> | `registry.generated.json` + `registry.generated.schema.json`, `--generated-registry-file`, `candidate`/`gated` gate semantics | #1367 |
> | `internal/awsmodel` — the shared Smithy AST reader (G1) | #1359 |
> | `compat/baseline.json` → `compat/baseline/<suite>.json` + a per-shard size budget | #1370 |
>
> **G1's pruned shape snapshot is also done**, but via
> [inert-tier-rollout.md](./inert-tier-rollout.md) Phase I1 rather than this
> plan — §3.7 said "build once, whichever plan gets there first", and that plan
> got there first. `models/aws/shapes/` and the `shapes-sha256` in
> [models/aws/VERSION](../../models/aws/VERSION) exist today; the pruner is
> `cmd/awsmodelgen/shapes.go`, and it is already a consumer of
> `internal/awsmodel`. **Do not build a second distillation.**
>
> **Still unimplemented: `cmd/compatgen` and `compat/model/`** — the IR and
> recipe schemas, `--scaffold`/`--review-report`/`--explain`, and `gaps.json`.
> That is the whole of what stands between here and the G2 pilot. G0's tail is
> also outstanding: the seven per-suite loaders and `compat/mcp.go` do not yet
> read the generated sibling, which is harmless only while it stays empty.
>
> **This section's counts are a 2026-08-03 snapshot and have drifted.**
> Recomputed 2026-08-23: `compat/suites/registry.json` is **140 groups / 790
> tests / 36 services**; the baseline is **5,404 entries** across eight shards
> (largest `dotnet-sdk.json` at 127,377 B of a 512 KiB per-shard ceiling) —
> 3,230 `pass`, 2,137 `skip`, 36 `unimplemented`, 1 `na`, **0 `fail`**;
> `compat/parity-debt.json` holds **325** entries. Capabilities totalled
> **1,434 rows — 1,240 Supported / 154 Unsupported / 28 Inert / 12 Partial**
> at the 2026-08-21 check (Auto Scaling promoted out of inert by #474, Backup
> made a real REST implementation via #815/#904, `StatusPartial` in live use).
> The alias table has 16 entries and has moved within `registry_data.go`, so
> the line-number citations below are approximate. Treat the generated
> artifacts named in this section as authoritative and recompute before
> acting; do not trust the prose numbers.

Counts below were computed from the checked-in generated artifacts, not from
`STATUS.md` — **`STATUS.md` prose is stale** (it describes Shield as "Stub — all
ops return 501" while `internal/capabilities/all.gen.go:*` declares five Shield
operations `StatusSupported`). Treat `internal/capabilities/all.gen.go`,
`internal/awsapi/manifest.gen.go`, `compat/suites/registry.json` and
`compat/baseline/` as the sources of truth.

### 2.1 The model universe

- [internal/awsapi/manifest.gen.go:1](../../internal/awsapi/manifest.gen.go) —
  generated by `cmd/awsmodelgen`, `SourceRevision =
  "66e973cadf6b6e909b200217d0d6065e49445a9a"`. **18,850 operations across 426
  modeled service identities** — 424 distinct keys after the alias table at
  [internal/awsapi/registry_data.go:71-84](../../internal/awsapi/registry_data.go)
  merges 52 identities onto Overcast's 50 registered service keys (the sibling
  plans count in identity space; both figures describe the same corpus).
- Of those, **4,440 operations belong to the 52 identities (50 Overcast
  services) that are registered**; 14,410 belong to 374 identities that are not.
- The per-operation metadata is deliberately routing-only —
  [internal/awsapi/manifest.go:37-50](../../internal/awsapi/manifest.go):
  `Service, ServiceShape, SDKID, APIVersion, Name, Protocol, Protocols,
  TargetPrefix, HTTPMethod, URI`. **There are no input or output shapes.**
- **The raw Smithy AST is not vendored.**
  [models/aws/VERSION](../../models/aws/VERSION) records
  `source/revision/model-date/manifest-sha256/license/format` and states that
  only the compact generated manifest is committed;
  [aws-api-operation-coverage.md §3](./aws-api-operation-coverage.md) explains
  why (size, and nothing at runtime needs it). Regeneration requires a local
  `api-models-aws` checkout at the pinned revision, supplied via
  `AWS_MODELS_DIR`, and the generator validates the match.

So: operation *names* and *protocols* are available offline today; input
members, required-ness, enums, constraints, output members, error shapes and
Smithy `resource` lifecycle bindings are **not**. §3.7 resolves that.

### 2.2 What the emulator claims

[internal/capabilities/all.gen.go](../../internal/capabilities/all.gen.go)
(capgen-generated) declares **1,318 operations across 50 services**:

| Status | Count |
| --- | --- |
| `StatusSupported` | 1,116 |
| `StatusUnsupported` (always 501) | 154 |
| `StatusInert` | 48 |
| `StatusWIP` / `StatusPartial` | 0 declared today |

The five statuses are defined at
[internal/capabilities/capabilities.go:17-27](../../internal/capabilities/capabilities.go)
— `StatusInert` already exists and is exactly Tier 1. Capgen already refuses a
non-`DocOnly` capability that no manifest operation backs
([cmd/capgen/main.go:244-258](../../cmd/capgen/main.go)), so the capability
table and the model are kept in agreement.

### 2.3 What compat actually measures

- [compat/suites/registry.json](../../compat/suites/registry.json) —
  **94 groups, 496 tests, 27 services, 466 distinct `(service, op)` pairs.**
  (The "~39 groups" figure in circulation is out of date.)
- Cross-referenced against the capability table:
  - **418 of 1,116 `StatusSupported` operations (37%) have a compat test.**
  - **900 declared capabilities have no compat test at all.**
  - **0 of the 48 `StatusInert` operations have a compat test.** The tier that
    exists specifically so CDK works is completely unmeasured by compat.
- [compat/baseline/](../../compat/baseline) — **3,367 entries, 532 KB** at the
  time of writing: 2,690 `pass`, 676 `skip`, 1 `na`, **0 `fail`** (the ratchet
  reached zero in #462 and `--max-failures 0` now asserts it absolutely). Since
  #1370 this is a directory of per-suite shards rather than a single
  `compat/baseline.json`; see the §2 note for current figures.
- [compat/parity-debt.json](../../compat/parity-debt.json) — 558 registry tests
  of debt, all in `rust-sdk` (297) and `dotnet-sdk` (261).

### 2.4 The harness is already registry-driven — this is the key enabler

Every suite loads `registry.json` and resolves a `TestName → impl` map, emitting
the shared sentinel for anything unimplemented:

| Suite | Loader |
| --- | --- |
| node-js-sdk | [src/lib/registry.ts:141-234](../../compat/suites/node-js-sdk/src/lib/registry.ts) |
| python-sdk | [lib/registry.py:35-125](../../compat/suites/python-sdk/lib/registry.py) |
| go-sdk | `internal/registry/registry.go` |
| cli | `internal/registry/registry.go` |
| java-sdk | `src/main/java/io/overcast/compat/registry/Registry.java` |
| dotnet-sdk | `Registry/RegistryLoader.cs` |
| rust-sdk | `src/registry.rs` |
| cdk | `src/runner.ts` (scoped groups only) |

The resolution rule is identical everywhere: try `"<group>:<test>"`, then the
bare test name, else emit `skip: not yet implemented in <suite> test suite`
([registry.ts:182-195](../../compat/suites/node-js-sdk/src/lib/registry.ts),
[registry.py:95-102](../../compat/suites/python-sdk/lib/registry.py)). **A
generic scenario interpreter is one extra fallback in that same lookup** — after
the hand-written impl, before the not-implemented sentinel. No suite
architecture changes.

Other facts the design leans on:

- `"suites": [...]` group scoping exists and is enforced by the parity checker
  ([registry.schema.json:39-43](../../compat/suites/registry.schema.json));
  out-of-scope suites carry neither an implementation nor parity debt.
- Group filtering already exists end to end: `compat/runner.go:336` passes
  `OVERCAST_COMPAT_GROUPS` to every suite subprocess, and all eight runners read
  it.
- Groups run 8-way parallel inside a suite
  (`OVERCAST_COMPAT_PARALLEL_SLOTS`); suite processes run **sequentially** in
  the local runner ([compat/AGENTS.md:780](../../compat/AGENTS.md)) but CI runs
  one job per suite in a matrix
  ([.github/workflows/compat.yml:200-216](../../.github/workflows/compat.yml)).
- `cmd/compat` has no group/test/shard filter flags today — only `--suite`
  ([cmd/compat/main.go:53](../../cmd/compat/main.go)). Sharding needs one new
  flag over the existing env var.
- Resource-name hygiene is a review rule, not tooling: every resource name must
  embed its group token, because sibling groups run concurrently and prefix
  collisions caused the whole #388 flake cluster
  ([compat-baseline-and-uniformity.md](./compat-baseline-and-uniformity.md)).
  Generation must obey it *by construction*.

---

## 3. Design

### 3.1 What generation is actually for

Framing that resolves most of the design tension: **the scenario generator is
the Tier 1 conformance test generator.**

A Tier 1 inert service is defined precisely by a Create → Describe/List
round-trip with field verification, an Update read-back, and a Delete absence
check — which is exactly the assertion contract's required-roundtrip table, and
exactly what a model plus a small recipe can produce. Behaviour (Tier 2) is what
generation *cannot* express, and that is what hand-written groups keep doing.

Three consequences:

- During rollout, generated coverage is **additive and clearly marked**: it does
  not displace a hand-written group until that group is deliberately ported and
  proven equivalent (§3.11); where both cover an operation meanwhile, the
  hand-written one is the richer test and the generated one is the
  shape/lifecycle floor.
- A generated group written against a Tier 0 service records `unimplemented`
  today and **starts passing the day the service reaches Tier 1, with no test
  edit**. That is the payoff: `inert-tier-rollout.md` gets its acceptance gate
  for free, per service, in eight clients.
- [services-never-emulated.md](./services-never-emulated.md) services get **no**
  generated groups (§3.9): their honesty mechanism is the server-side 501 corpus
  plus the `NeverEmulated` policy marker on the dashboard, not permanent
  `unimplemented` rows.

### 3.2 D1 — Generation target: a scenario IR with two backend families (hybrid)

**Recommendation: one declarative scenario IR, executed by an interpreter in the
dynamically-invokable tools and compiled to source for the statically typed
ones.**

| Suite | Backend | Why |
| --- | --- | --- |
| `python-sdk` | **interpreter** | boto3 is dynamic *in production*: `boto3.client(svc)` + `getattr(client, xform_name(op))(**params)` is the ordinary public API, so the interpreter exercises the identical serialization path a real app does. botocore also ships the full service model locally, which the interpreter can use for input coercion. |
| `node-js-sdk` | **interpreter** | `new mod[`${Op}Command`](params)` against a generated service→module import map. Command classes are the public API; no private surface touched. |
| `cli` | **interpreter** | `aws <cli-service> <kebab-op> --cli-input-json '<json>'`. `--cli-input-json` accepts exactly the modeled input JSON, so one code path covers every operation with zero per-op flag mapping. |
| `go-sdk`, `java-sdk`, `dotnet-sdk`, `rust-sdk` | **generated source** | No public dynamic-dispatch API exists. Emit one test function per scenario step into `*_gen.go` / `*Gen.java` / `*Gen.cs` / `*_gen.rs`, compiled by the normal build. |

**Rejected: a generic typed-SDK invoker built on each SDK's protocol/marshaller
layer** (smithy-go middleware stacks, the Java SDK's internal marshallers). It
would be less code, but those APIs are internal and unstable, and using them
breaks the first core principle — *tests use the SDK exactly as production code
would* ([compat/AGENTS.md:78-81](../../compat/AGENTS.md)). The entire value of
running eight suites is that each exercises its own real typed serialization
path; a shortcut there deletes the reason the suite exists.

**Rejected: per-language source codegen everywhere** (no IR). It fixes nothing —
seven emitters instead of one IR plus three interpreters, and every recipe change
regenerates megabytes of source in seven languages.

**Rejected: an IR-only approach with no typed backends** — it would leave four
suites permanently behind, which the uniformity policy correctly treats as debt.

Debuggability is the interpreter approach's real cost, and it is paid explicitly:

- Every interpreter failure message must carry `group/test`, the operation, the
  **exact params JSON sent**, the assertion kind, expected vs actual, and the
  scenario file + step index.
- `go run ./cmd/compatgen --explain <group>/<test> --lang python` renders a step
  as idiomatic pseudo-code in any target language, so a failing generated test
  can be reproduced by hand in seconds.
- The typed backends generate readable source, so four of eight suites are
  greppable by construction — which also serves as a cross-check that the IR
  means what the interpreters think it means.

### 3.3 D2 — Passing constructs between tests: recipes, exports, bindings

This is the part the model cannot solve alone, so the design is explicit about
where the machine stops and a human starts.

**What Smithy gives us:** operation names, input/output *members* with types,
required-ness, enums, length/pattern/range constraints, error shapes, and — in
the raw AST — `resource` shapes with lifecycle bindings (`create`, `read`,
`update`, `delete`, `list`) and identifier members.
**What Smithy never gives us:** legal *values*, cross-service semantics (a
Lambda function needs an IAM role ARN that must look like a role ARN), ordering
constraints, or which fields are server-assigned.

So: **structure is generated, semantics are curated.**

#### Recipes (hand-curated, one file per service, model-scaffolded)

`compat/model/recipes/<service>.json`, schema-validated:

```jsonc
{
  "service": "sqs",
  "resources": [{
    "id": "queue",
    "create":  { "op": "CreateQueue", "params": { "QueueName": { "$name": "q" } } },
    "exports": { "url": "$.QueueUrl" },
    "derived": [{ "export": "arn",
                  "op": "GetQueueAttributes",
                  "params": { "QueueUrl": { "$ref": "queue.url" },
                              "AttributeNames": { "$lit": ["QueueArn"] } },
                  "path": "$.Attributes.QueueArn" }],
    "binds":   { "QueueUrl": "queue.url", "QueueArn": "queue.arn" },
    "read":    { "op": "GetQueueAttributes", "identityPath": "$.Attributes.QueueArn" },
    "list":    { "op": "ListQueues", "itemsPath": "$.QueueUrls", "identityPath": "$" },
    "delete":  { "op": "DeleteQueue" },
    "notFound": { "errorCode": "AWS.SimpleQueueService.NonExistentQueue" },
    "mutable": [{ "member": "Attributes.VisibilityTimeout", "from": "30", "to": "60" }],
    "requires": []
  }]
}
```

- `binds` is the reference model: an input member name → a context-bag path.
  This is what makes `create-bucket` before `put-object`, `role ARN` before
  `create-function`, `subnet` before `instance` work — `requires: ["iam.role"]`
  on `lambda.function`, and `binds: {"Role": "iam.role.arn"}`.
- The generator **scaffolds** a recipe skeleton (`--scaffold <service>`) from
  the model: proposed resources from Smithy `resource` shapes where present,
  else from Create/Describe/List/Delete name clustering; required members
  pre-listed; identifier members guessed. A human fills in values and reviews.
  Scaffolding is a time-saver, never an authority.
- `mutable` is required before any Update-family operation is generated — see
  §3.4.
- Setup instantiates a group's recipes in topological `requires` order;
  teardown reverses it, wrapping each delete individually, per the canonical
  teardown rules.

#### Value expressions (closed, tiny, total)

`$lit`, `$ref` (context path), `$name` (unique name, always
`{runId}-{groupToken}-{suffix}` so name hygiene holds by construction),
`$concat`, `$index`. No conditionals, no scripting, no arithmetic — eight
implementations must agree exactly. Path syntax is dot-plus-numeric-index only
(`$.Attributes.QueueArn`, `$.Queues[0].Name`), not full JSONPath.

#### Parameter binding algorithm (generator, offline, deterministic)

For each modeled required input member, in order:

1. an explicit `binds` entry in an in-scope recipe → bind;
2. an exact name match against an in-scope recipe export → bind, and **record
   the automatic binding in the diff** so review sees it;
3. a curated literal in `compat/model/values.json`, keyed
   `(service, op, member)` then `(shapeName)` then `(memberName)`;
4. a constraint-derived synthetic value (first enum member; shortest legal
   string for a pattern; range minimum) — only for scalars;
5. otherwise **refuse**. The operation is not generated and appears in
   `compat/model/gaps.json` with a machine-readable reason
   (`unbound-required-member:RoleArn`).

Optional members are left unset except the single `mutable` member on Update
operations. Refusal is the default and is cheap to fix (one line in a recipe);
guessing is never allowed.

#### Grouping and naming

- Lifecycle groups: `<service>-gen-<resource>` (`sqs-gen-queue`) — kebab-case,
  matching the existing schema pattern.
- Probe groups: `<service>-gen-probe` (§3.5).
- Test names: the PascalCase operation name; variants fold into the name with
  the bare operation in `op` (`CreateQueueWithTagsAtCreate`, `op:
  "CreateQueue"`) exactly as the registry rules require.
- Each group is independently runnable: it creates its own recipes and destroys
  them; nothing crosses group boundaries.

### 3.4 D3 — Assertion generation

Derivable from the model: which output member carries identity, which members
echo input, the error shapes an operation can raise, and (from the AST resource
bindings) which operation reads a resource back. Not derivable: which fields are
*semantically* comparable versus server-assigned — so the recipe declares
`read`, `list`, `identityPath`, `notFound` and `mutable`, and the generator does
the rest.

The IR has a closed set of assertion kinds:

| Kind | Emitted for | Contract clause satisfied |
| --- | --- | --- |
| `responseField` | any op — path exists / non-empty / equals / matches | "Create must assert ARN/ID non-empty, name matches" |
| `readback` | `Create*`/`Put*`/`Update*`/`Set*`/`Tag*` | Create→Describe, Update→read-back |
| `listContains` | `Create*` | "List must be non-empty and contain the created resource" |
| `absent` | `Delete*`/`Untag*` | Delete→absence, or the declared not-found error |
| `errorCode` | negative-path variants | assertion-contract exception 2 |
| `eventually` | wraps any of the above, bounded `maxAttempts`/`delayMs` | "no sleep/polling unless strictly necessary" — only when the recipe declares the resource async |

Emission rules:

- `Create*`/`Put*` → `responseField` (identity non-empty) **+** `readback` via
  the recipe's `read` **+** `listContains` via the recipe's `list`.
- `Update*`/`Set*`/`Tag*` → the mutated member is set from `mutable.to`, then
  `readback` asserts the read path now equals `to`. Without a `mutable`
  declaration the operation is **refused**, which is what prevents the
  "asserting the ID is still non-nil" anti-pattern the contract calls out.
- `Delete*`/`Untag*` → `absent`, using `notFound.errorCode` if declared, else
  `list` non-membership.
- `Get*`/`Describe*`/`List*` standing alone → generated only inside a lifecycle
  group where the resource was created in the same group, asserting identity.
  A bare read of nothing is refused.
- An operation with no read-back path and no declared `shapeAssert` (the
  contract's exception 1 — `GenerateDataKey`-style) is **refused**.

**Invariant, enforced in the generator and by a CI lint: every emitted test
carries at least one assertion clause.** A vacuous generated test cannot be
represented in the IR.

### 3.5 D4 — Purposefulness guard

Structural guards (the generator physically cannot emit the bad cases):

1. No assertion clause → no test (§3.4).
2. Unbound required member → no test (§3.3).
3. Update without a declared mutation → no test.
4. **Probe groups may only contain operations the emulator does not implement**
   — i.e. modeled operations that are absent from
   `internal/capabilities/all.gen.go` or declared `StatusUnsupported`. A probe
   test calls the operation once with model-valid literals and asserts the
   modeled output identity member; against a Tier 0 service the SDK raises the
   501 and the harness records `unimplemented`, which is the correct and
   informative result. An implemented operation is never allowed in a probe
   group; regeneration moves it into a lifecycle group or refuses it. This is
   what stops "10,000 tests that assert nothing".

Review guards (humans review the curated layer, not 5,000 JSON blobs):

- **One service per PR**, mirroring the parity-backfill cadence that worked.
- The reviewable artifacts are the **recipe file**, the **values entries**, and
  the **gap report** — a few hundred lines. The generated IR and registry are
  derived and diff-visible but reviewed by exception.
- `cmd/compatgen --review-report` prints, for the PR body: operations covered
  vs modeled, every refusal with its reason, every *automatic* name-match
  binding (rule 2 in §3.3 — the riskiest inference), and N randomly sampled
  scenarios rendered as pseudo-code.
- CI lint `compat-gen-check`: regen-and-diff; assertion-clause invariant; probe
  membership; naming rules; no generated group duplicates a hand-written
  group's `(group, test)` key; `$name` used for every user-supplied identifier.

Quality bar for accepting a service's generation, stated so a reviewer can apply
it: *would a competent human, writing this test by hand against real AWS, assert
the same thing?* If the generated assertion is weaker than the recipe could
express, fix the recipe. If it is weaker than the model allows, that is a
generator bug.

### 3.6 D5 — Registry mechanics, coexistence, and the CI gates

#### Where generated groups live

**A generated sibling file**, `compat/suites/registry.generated.json`, validated
by `registry.generated.schema.json` (which `$ref`s the shared `TestGroup`
definition and adds `generated`, `scenario`, `state`). Every loader
concatenates the two files; `cmd/compat` gains `--generated-registry-file`.

Rationale: the hand-written registry stays reviewable (a 5,000-entry diff would
drown the file that humans edit); the generator can rewrite its own file wholly
without merge conflicts; and "generated vs hand-written" is unambiguous for the
dashboard, the report and the lint. Baseline, flaky and parity-debt files key on
`suite/group/test` and are indifferent to which file a group came from.

#### Suite scoping

Generated groups carry an explicit `"suites": [...]` listing the backends that
can execute them — the three interpreters first, widening as each typed backend
lands. This reuses the tested scoping mechanism and keeps the parity checker
honest with zero new concepts: a suite without a backend neither implements the
group nor carries debt for it.

**This deviates from [compat/AGENTS.md:631-636](../../compat/AGENTS.md)** ("an
SDK suite is never a legitimate `suites` scope"). The amendment must land with
Phase G0 and must be narrow: *`suites` scoping on a **generated** group is
mechanically derived from backend availability and is never hand-edited; on a
hand-written group it remains reserved for `cdk-lifecycle`.* A lint enforces
both halves.

#### Candidate → gated soak (the flake defence)

New generated groups land with `"state": "candidate"`. Candidate groups are
**excluded from both `--compare-baseline` and `--max-failures`** — the inverse
of `flaky.json`: quarantined by default until they prove themselves, rather than
gated by default until someone gets a label.

| Stage | Rule | Enforced by |
| --- | --- | --- |
| Candidate | Runs everywhere, reports everywhere, gates nothing | `state` in the generated registry |
| Soak | The existing nightly 3× flake-detection job includes candidates | [compat-flake-detection.yml](../../.github/workflows/compat-flake-detection.yml) |
| Promotion | A group with 3 identical consecutive nightly results and **zero** `fail` flips to `gated`, via a bot PR on the existing `automation/` pattern | new `--promote-generated` in `cmd/compat` |
| Stuck | Inconsistent groups stay candidate and raise the usual per-(suite, group) issue; a candidate older than 30 days is reported overdue | [scripts/compat-flake-issue.py](../../scripts/compat-flake-issue.py) |

Promotion is mechanical, so it needs no reviewer label — the reviewer decision
already happened at recipe review. A generated test that fails because the
*test* is wrong is fixed in the recipe or values table, **never** by weakening
an assertion and never by adding it to `flaky.json`.

#### Volume: baseline, parity, CI runtime, dashboard

- **Baseline size.** ~~3,367 entries / 532 KB today.~~ **Done in #1370.** Tier-1
  fleet coverage plausibly reaches 3,000–4,000 generated tests × the suites in
  scope, i.e. a five-to-tenfold increase, so the file was sharded to
  `compat/baseline/<suite>.json` in **G0, while it was still small**, keeping
  the format and the lint semantics. The per-shard budget is **512 KiB**, about
  4x the largest shard today — tripping it means sharding further, by service,
  not raising the number. `--baseline-file` still accepts a single file, so a
  base commit older than the split remains lintable against. (Considered and
  deferred: dropping `pass` entries to shrink the file — it would weaken the
  "failing and absent from the baseline" check that #462 relies on.)
- **Parity debt.** Generated groups must not inflate debt: `suites` scoping means
  a suite without a backend is out of scope, not indebted. Open question §7.5:
  whether a suite with outstanding *hand-written* debt (rust-sdk 297,
  dotnet-sdk 261) should be blocked from receiving a generated backend until it
  clears — the debt file only shrinks, and mixing the two flows would obscure
  that.
- **CI runtime.** Add `--shard i/n` to `cmd/compat`, implemented over the
  existing `OVERCAST_COMPAT_GROUPS` plumbing (`compat/runner.go:336`) so no
  suite changes are needed. PR runs execute all hand-written groups plus one
  rotating shard of gated generated groups; the nightly runs everything. The
  `cli` suite spawns one process per API call and is already the slowest matrix
  job, so its shard count is tuned independently.
- **Dashboard.** Add a `generated` facet; default the matrix to hand-written
  groups; make **model coverage** (`operations with a test / operations in the
  service's tier`) the headline metric, per service and per tier. A matrix of
  5,000 rows is not a UI — a coverage meter with drill-down is.

### 3.7 D6 — Where the generator lives, and the boundary

**Rule, stated once and enforced: nothing under `compat/` imports a Go package
from the emulator tree.** That is the boundary
([compat/AGENTS.md:35-48](../../compat/AGENTS.md)) and it is untouched.

- The generator is **`cmd/compatgen`**, a build-time command in the main module
  beside `cmd/awsmodelgen` and `cmd/capgen`. It reads the pinned Smithy AST from
  a local `api-models-aws` checkout via `AWS_MODELS_DIR`, validating against
  [models/aws/VERSION](../../models/aws/VERSION) exactly as
  `make generate-aws-operations` does. It shares the AST reader with
  `cmd/awsmodelgen` (extract it to `internal/awsmodel` in G1) and may read
  `internal/capabilities` for tier classification. It is a tool, not a suite.
- Its outputs are **committed data files** read by suites through ordinary file
  I/O in each language — the same relationship suites already have with
  `registry.json`:

  | Artifact | Purpose |
  | --- | --- |
  | `models/aws/shapes/<service>.json` | the pruned shape snapshot: distilled input/output members, required-ness, enums, constraints, error shapes, and resource lifecycle bindings — **for the allowlisted services only**. **Shared with [inert-tier-rollout.md](./inert-tier-rollout.md) §4.6** — one pruner (in `cmd/awsmodelgen`), one `shapes-sha256` in `models/aws/VERSION`, two consumers (`cmd/awsmodelgen -inert-*` and `cmd/compatgen`); do not build a second compat-local distillation |
  | `compat/model/recipes/<service>.json` | hand-curated (input, not output) |
  | `compat/model/values.json` | hand-curated literal table (input) |
  | `compat/model/scenarios/<service>.json` | generated scenario IR |
  | `compat/model/gaps.json` | refusal report |
  | `compat/suites/registry.generated.json` | generated registry sibling |
  | `compat/suites/*/src/**/scenarios_gen.*` | generated source, typed backends only |

- **Do we extend the vendored snapshot?** Yes, but as a *distillation*, not the
  raw AST — and it is the **same** pruned snapshot
  [inert-tier-rollout.md](./inert-tier-rollout.md) §4.6 specifies
  (`models/aws/shapes/`, built by the `cmd/awsmodelgen` pruner in that plan's
  Phase I1); this plan adds a consumer, not a second corpus. The snapshot is
  scoped to the union of both plans' allowlists (§3.9 here) and carries
  only the fields §3.3/§3.4 consume. This preserves the snapshot policy's intent
  (the raw corpus stays out of the tree, nothing at runtime parses models) while
  giving the generator the shape data the routing manifest deliberately omits.
  A size budget is part of the acceptance gate. Smithy `resource` shapes *are*
  vendored into `shapes.json` because they are the single best source of
  lifecycle bindings and they make scaffolding materially better.
- **Regeneration workflow.** Hook into the existing weekly model-refresh
  automation ([aws-api-operation-coverage.md §8](./aws-api-operation-coverage.md)):
  when the pinned revision moves, the same bot PR regenerates `shapes.json`,
  the scenarios and the generated registry, and reports operations added/removed
  per service. New operations arrive as **candidate** generated tests, so they
  can only ever be `unimplemented` or `pass` — a model refresh can never break
  the gate. Offline PRs verify a `shapes-sha256` recorded in
  `models/aws/VERSION`, mirroring the existing `manifest-sha256` check.

### 3.8 D7 — IaC suites: out of scope, deliberately

CDK, Terraform/OpenTofu and Pulumi deploy whole stacks; their unit of
observation is a stack lifecycle, not an operation, which is exactly why
`cdk-lifecycle` already uses `suites` scoping. **Model-driven per-operation
generation does not apply to them, and generated groups always exclude them.**

The IaC analogue is a different generator against a different model — CDK L1
constructs and Terraform resources map to CloudFormation resource types, whose
schemas live in the CloudFormation resource-schema registry, not in Smithy. A
"minimal stack per resource type" generator is plausible and valuable (it is the
most direct Tier 1 acceptance test there is), but it is a separate plan, needs a
second pinned model, and must not be smuggled into this one. Named here so
nobody re-derives the question; explicitly deferred.

What the IaC suites *do* get from this plan: the Tier 1 operations that CDK
depends on become measured in eight clients, so a CDK deploy failure can be
attributed to a specific operation's shape rather than bisected by hand.

### 3.9 Scope: which operations get generated

Generating all 18,850 modeled operations would be absurd. The allowlist is
tier-driven:

| Population | Treatment |
| --- | --- |
| Operations of the 50 registered services (**4,440**) | Full generation: lifecycle groups for implemented/inert resources, probe groups for the rest |
| Services promoted to Tier 1 by [inert-tier-rollout.md](./inert-tier-rollout.md) | Added to the allowlist as they are promoted |
| Services never-listed or deferred by [services-never-emulated.md](./services-never-emulated.md) | **Not generated — no probe groups.** (This supersedes an earlier draft's "capped probe group" idea, reconciled with that plan's §5.2: permanent `unimplemented` rows would read as roadmap gaps and never change.) Ownership and 501-envelope correctness are guaranteed and tested server-side by [aws-api-operation-coverage.md](./aws-api-operation-coverage.md)'s corpus; SDK-side 501-envelope parsing is already exercised by the probe groups of registered-but-unimplemented operations in every protocol family. Dashboard visibility comes from the `NeverEmulated` policy marker (that plan's §5.3), which the coverage report renders as "N/A by policy" rather than as a gap |
| Everything else (374 unregistered identities, 14,410 operations) | Not generated until [inert-tier-rollout.md](./inert-tier-rollout.md) promotes them onto the allowlist. Same server-side coverage argument as above |

### 3.10 Performance and fidelity constraints

Both are explicit repo values and both bite here.

**Performance.**

- Generation is entirely build-time. Nothing parses a model at test time: the
  interpreters read a per-service scenario file and execute steps. Per-step
  overhead must stay negligible against SDK + network time; the `cli` suite's
  process-spawn cost dominates and is the reason for independent shard tuning.
- CI wall-clock is a first-class acceptance gate on every phase, not an
  afterthought. A phase that adds more than its budgeted minutes to the matrix
  does not land until it is sharded.
- Generated runs are a fine **latency observatory** — `duration_ms` is already
  in the wire format, so a `--slowest N` report gives a fleet-wide per-operation
  latency census for free. They are **not** a benchmark gate: CI runners are too
  noisy, and performance claims still require the paced local methodology in
  [storage-test-plan.md](./storage-test-plan.md).

**Fidelity.**

- No suite may gain an Overcast-specific code path to make generated tests work.
  The endpoint override remains the only deviation from production
  configuration. If a generated test cannot be expressed through the public SDK
  API, it is refused.
- Generated values must be AWS-legal, not merely emulator-accepted. When a
  generated test passes against Overcast the natural next question is whether it
  would pass against AWS; the values table should be reviewed with that question
  in mind, and any operation where the honest answer is "no" belongs in the gap
  report.
- Refusals are a feature. `gaps.json` is a public, reviewed statement of what
  the model cannot mechanically express — it is far more valuable than a test
  that passes for the wrong reason.

### 3.11 Endgame — IR-first; native test code is the audited exception

Steady state has **three layers**, in strictly decreasing volume and strictly
increasing human involvement:

| Layer | Authored by | Volume | Human touch |
| --- | --- | --- | --- |
| **Model-generated scenarios** | `cmd/compatgen` from shapes + recipes | thousands | recipe/values review once per service; regeneration is free |
| **Authored scenarios** | a human, **in the IR**, once | hundreds | the scenario file itself is the review artifact — one spec replaces eight per-language implementations |
| **Native per-suite tests** | a human, per language | tens, capped | each entry requires a reason in a checked-in exceptions file |

Behavioural intent that the model cannot know (send a message, receive it,
assert the body; publish to a topic subscribed to a queue and poll; FIFO
ordering; DLQ redrive) is not lost and not machine-guessed — it is written **by
hand in the IR**, where the existing step/assertion vocabulary (`eventually`,
`readback`, `errorCode`, cross-resource `$ref`s across recipes) already
expresses most of today's hand-written groups. The economics change from
"behavioural test = 8 implementations to keep in sync" to "behavioural test =
1 spec, executed by every backend".

**The native exception list** is for what the IR structurally cannot express,
and it must stay short. Expected categories, each requiring a listed reason:
streaming/chunked request bodies; presigned-URL flows exercised outside the SDK
client; deliberately malformed wire traffic below the SDK's public surface; and
a small deliberate **idiom suite** — paginators, waiters, high-level layers
like boto3 resources or the DynamoDB DocumentClient — kept native *because*
those exercise SDK client code paths the interpreter/generated-source path does
not touch. An entry without a reason, or a reason the IR has since learned to
express, fails the lint.

**Migration of the existing 94 groups / 496 tests**, group by group, any time
after the relevant backends exist (G3):

1. Author the IR scenario under the **same registry group/test names** — the
   names are the join keys, so baseline history, dashboard history, and
   flaky/debt bookkeeping survive untouched.
2. Run both implementations in parallel through one nightly soak cycle; every
   (suite, test) result must match its native predecessor exactly.
3. Delete the per-language implementations in the same PR that flips the group's
   resolution to the scenario. A divergence blocks the deletion — never the
   gate — and is triaged as either an IR expressiveness gap (extend the IR or
   add a native exception) or a latent bug in one of the eight copies (fix it;
   this migration is precisely how such divergences get found).
4. A ported group implemented by scenario counts as implemented in **every**
   suite with a backend — which is how `rust-sdk`'s 297 and `dotnet-sdk`'s 261
   parity-debt entries are burned down without anyone hand-porting them
   (resolving §7.5 in favour of "generation lands first").

**The human-input budget is a design constraint, not a hope.** Humans author
recipes, values, authored scenarios, and the exceptions file; review
concentrates on those four artifacts and on `gaps.json` as the exception queue.
Everything else — regeneration, soak, promotion, coverage accounting — is
mechanical. Two tripwires keep the budget honest: a per-service ceiling on
`gaps.json` entries (a service whose refusal list keeps growing means the
generator is missing a capability — fix the generator, don't grind the queue),
and the exceptions-file lint above. When a new operation appears in a model
refresh, the intended cost is zero human actions for shape coverage and one
reviewed scenario only if it warrants behavioural coverage.

---

## 4. First milestone — pilot (Phase G2)

Two services, chosen to exercise both ends of the tier ladder.

### 4.1 `sqs` — validate against real, known-good behaviour (Tier 2)

- 23 modeled operations
  ([internal/awsapi/manifest.gen.go](../../internal/awsapi/manifest.gen.go)),
  21 declared capabilities (19 `StatusSupported`, `AddPermission` and
  `RemovePermission` `StatusUnsupported`).
- Four hand-written groups exist today — `sqs-queues`, `sqs-messages`,
  `sqs-dlq`, `sqs-fifo`, 21 tests — implemented in every suite and passing.
  **They are not touched.** The pilot proves generated coverage is additive and
  that the generator's assertions agree with hand-written ones where they
  overlap.

Acceptance criteria:

1. `sqs-gen-queue` and `sqs-gen-message` are generated from one reviewed
   `recipes/sqs.json` plus at most 15 lines of `values.json`.
2. **≥ 20 of the 23 modeled operations** are covered by generated tests; every
   refusal appears in `gaps.json` with a specific reason.
3. Every generated test has ≥ 1 assertion clause; the two `StatusUnsupported`
   operations land in `sqs-gen-probe` and record `unimplemented`.
4. Passes in `python-sdk`, `node-js-sdk` and `cli`, with **identical results
   across three consecutive runs** (`scripts/compat-flake-detect.py`).
5. Zero trace: a `ListQueues` sweep after the run finds no `{runId}` resource.
6. No generated `(group, test)` key collides with a hand-written one, and the
   four hand-written groups' results are byte-identical to the previous
   baseline.

### 4.2 `organizations` — prove the unimplemented path (Tier 0 → Tier 1)

Chosen because it is the cleanest instance of the problem: **63 modeled
operations, exactly one declared capability** — `DescribeOrganization`,
`StatusInert`
([internal/capabilities/all.gen.go:1046](../../internal/capabilities/all.gen.go))
— and **zero compat coverage today**. It is a Tier 1 candidate in
`inert-tier-rollout.md`, so the pilot doubles as that plan's acceptance rig.

> **Moved since this was written (checked 2026-08-23).**
> [inert-tier-rollout.md](./inert-tier-rollout.md) Phase I2 (#1376) landed the
> shared Tier 1 runtime and proved it on `organizations` policies, so the
> service now declares **nine** `StatusInert` operations, not one:
> `CreatePolicy`, `DeletePolicy`, `DescribeOrganization`, `DescribePolicy`,
> `ListPolicies`, `ListTagsForResource`, `TagResource`, `UntagResource`,
> `UpdatePolicy`.
>
> This does not invalidate the pilot — it *improves* it, and the criteria below
> need recounting rather than rewriting. The probe group covers 54 undeclared
> operations, not 62. More usefully, criterion 5 — the regeneration
> demonstration that justifies the whole plan — no longer needs a hypothetical
> future operation to move: eight operations already crossed from undeclared to
> `StatusInert` since this plan was written, so the demonstration can be run
> against a move that has actually happened. The policy resources also give the
> recipe a real Create → Describe/List → Update → Delete lifecycle to express,
> where before there was only a single read. Recount against
> `internal/capabilities/all.gen.go` when starting G2; do not trust the figures
> in this section.

Acceptance criteria:

1. `organizations-gen-probe` covers the 62 undeclared operations; **all record
   `unimplemented`, none records `fail`**, in all three interpreter suites.
2. `organizations-gen-organization` exercises `DescribeOrganization` with a
   shape assertion (identity member present and ARN-shaped) and **passes** — the
   first compat coverage any `StatusInert` operation has ever had.
3. The group is independently runnable, creates nothing, and needs no teardown.
4. Three consecutive runs are identical; the groups promote from `candidate` to
   `gated` through the normal soak with no hand edits.
5. **The demonstration that justifies the whole plan:** when
   `inert-tier-rollout.md` implements the next `organizations` operations,
   regeneration alone moves them out of the probe group into a lifecycle group
   and their status flips from `unimplemented` to `pass` — with **zero
   hand-written test changes in any suite**. Show this end to end on at least
   one operation before declaring the pilot complete.

### 4.3 Pilot budget

The two services add **≤ 90 s** to a full local run and **≤ 2 min** to the
slowest CI matrix job. Exceeding that means sharding lands before rollout, not
after.

---

## 5. Phasing

Status as of 2026-08-23 is in the §2 note: **G0 is done bar its loader tail,
and G1 is done bar `cmd/compatgen`.** The `Status` column below records that;
`Contents` is left as written so the original scope stays legible.

| Phase | Status | Contents | Effort | Acceptance gate |
| --- | --- | --- | --- | --- |
| **G0** Foundations | **Done**, bar the loader tail (#1356, #1357, #1367, #1370) | Shard `compat/baseline.json` → `compat/baseline/<suite>.json` (+ size budget); `--shard i/n` and `--generated-registry-file` in `cmd/compat`; `registry.generated.schema.json`; all 8 loaders read the generated sibling and fall back to a scenario resolver hook; `candidate`/`gated` state honoured by both gates; `compat/AGENTS.md` amendment for generated `suites` scoping + the lint that bounds it | M | With an **empty** generated registry, every gate, report and dashboard behaves exactly as today; baseline shards aggregate byte-identically; the scoping lint rejects a hand-written group that adds `suites` |
| **G1** Model layer | **Partly done** — `internal/awsmodel` #1359, shape snapshot via inert-tier I1; `cmd/compatgen` outstanding | Extract `internal/awsmodel` AST reader; `cmd/compatgen` skeleton; the pruned shape snapshot `models/aws/shapes/` + `shapes-sha256` (shared deliverable with [inert-tier-rollout.md](./inert-tier-rollout.md) Phase I1 — build once, whichever plan gets there first); IR + recipe JSON schemas; `--scaffold`, `--review-report`, `--explain`; `gaps.json` | M | `make compat-model-check` regenerates byte-identically offline; the sha gate catches a hand edit; the snapshot is within its size budget; scaffolding a service produces a recipe skeleton a human can complete |
| **G2** Pilot | Not started | `python-sdk`, `node-js-sdk` and `cli` interpreters; `recipes/sqs.json` + `recipes/organizations.json`; the §4 acceptance criteria | L | Every §4.1 and §4.2 criterion met, including the regeneration demonstration in §4.2.5 |
| **G3** Typed backends | Not started | Source emitters for `go-sdk`, then `java-sdk`, `dotnet-sdk`, `rust-sdk` (one suite per PR); member→field naming rules per language | L each | Generated source compiles in the suite's normal build; the pilot groups produce **identical** results to the interpreter suites; generated `suites` scoping widens automatically on regeneration |
| **G4** Tier-1 fleet rollout | Not started | One service per PR, ordered by [inert-tier-rollout.md](./inert-tier-rollout.md) then [full-emulation-priority.md](./full-emulation-priority.md); capped probe groups for [services-never-emulated.md](./services-never-emulated.md) | L, parallelizable per service | Per service: recipe reviewed, no unexplained refusal in `gaps.json`, soak passed, CI wall-clock within budget, coverage metric moves |
| **G5** Steady state | Not started | Weekly model-refresh PR regenerates scenarios; coverage becomes the dashboard headline; `--slowest N` latency census | S | A model-refresh PR shows added/removed operations per service and cannot break the gate; coverage per service/tier is published |
| **G6** Native-group migration (§3.11; overlaps G4/G5, starts any time after G3) | Not started | Port the existing 94 hand-written groups to authored IR scenarios, group by group: same registry names, one parallel soak cycle, results must match, then delete the per-language code. Exceptions file + lint for what stays native (streaming, presigned flows, the idiom suite). | L, parallelizable per group | Per group: soak-parity with the native predecessor, native code deleted, registry names unchanged; fleet-wide: rust/dotnet parity debt reaches zero via backends, the exceptions file is the only remaining native test code and every entry carries a reason |

Every phase begins with a failing check, lands as small independently
reviewable PRs, and leaves `main` green under both existing gates.

---

## 6. What "done" means

Done means all of the following hold simultaneously:

1. **Coverage is model-relative and published.** For every service in the
   allowlist, "operations with a compat test / operations in that service's
   tier" is a computed number on the dashboard, and for Tier 1 and Tier 2
   services it is at or near 100%.
2. **Adding an operation to the emulator costs one recipe line, not eight test
   files.** Implementing a new operation means: declare the capability,
   regenerate, review the diff. Eight suites gain the test.
3. **Refreshing the AWS model surfaces new operations automatically**, as
   candidate tests that can never break the gate.
4. **No generated test can be vacuous.** The IR cannot express a test without an
   assertion, and CI proves the invariant on every PR.
5. **The gate is still absolute and still trusted.** `--max-failures 0` holds,
   `flaky.json` is still empty or shrinking, and no generated test was ever
   quarantined to make a run green.
6. **Native per-suite test code is the audited exception, not the medium.**
   Behavioural depth lives in authored IR scenarios (written once, executed by
   every backend); the exceptions file is short, linted, and every entry has a
   reason. No test exists in eight hand-maintained copies, and adding a
   behavioural test costs one reviewed scenario, not eight implementations.
7. **The boundary is intact.** Nothing under `compat/` imports emulator Go code;
   the generator is a build-time tool whose output is committed data.

---

## 7. Open questions

1. **`shapes.json` size budget and contents.** How much of the Smithy shape
   graph must be distilled before recursive member types force effectively the
   whole AST into the tree? Propose a depth limit and measure on the allowlist
   during G1 — if it does not fit comfortably, the alternative is deriving
   shapes at generation time from a required `AWS_MODELS_DIR` checkout and
   committing only the scenarios (losing offline scaffolding, keeping offline
   test execution).
2. **The `suites`-scoping amendment** to `compat/AGENTS.md` (§3.6) changes a rule
   that currently reads as absolute. It needs explicit reviewer agreement before
   G0 lands, not after.
3. **One name-mapping table, four consumers.** Smithy service → Overcast key →
   `aws` CLI command name → npm package (`@aws-sdk/client-*`) → Go module path →
   Java/C#/Rust client class. The alias table at
   [internal/awsapi/registry_data.go:71-84](../../internal/awsapi/registry_data.go)
   covers only the first mapping. Where does the full table live, and what
   detects drift when an SDK renames a package?
4. **Should `na` be derivable?** When the CLI or an SDK genuinely lacks an
   operation, the registry wants `na`. That is partly mechanical (botocore knows
   what the CLI exposes) and partly not. Deriving it would remove a class of
   hand-maintained divergence; getting it wrong would silently erase coverage.
5. **Generated backends versus outstanding parity debt — resolved by §3.11.**
   Generation lands first; the G6 migration then burns `rust-sdk`'s 297 and
   `dotnet-sdk`'s 261 debt entries down as groups are ported to scenarios, since
   a scenario-backed group is implemented in every suite with a backend. Nobody
   hand-ports those 558 tests. The residual question is only sequencing detail:
   whether the debt file should distinguish "awaiting backend" from "awaiting
   port" while G6 is in flight.
6. **Baseline format at 10× scale.** Sharding buys headroom; it may not be
   enough at full Tier-1 coverage. Compressing, or making `pass` the implied
   default, both weaken the "failing and absent from the baseline" check that
   #462 depends on. Decide with measured file sizes at the end of G4, not now.
7. **Registry `service` keys are unvalidated.** Nothing asserts that a
   `registry.json` group's `service` matches an Overcast capability service key
   (the `cognito` case works only because
   [registry_data.go:76](../../internal/awsapi/registry_data.go) aliases
   `cognito-identity-provider`). Generated groups will use the capability key by
   construction; a lint should hold hand-written groups to the same rule, and it
   is cheap to add during G0.
