# Model-driven compat coverage — scenario generation across every suite

> Status: **in progress** — G0 complete, G1 landing, G2 not started but
> unblocked once #1709 lands; #1700 is done. See the § 2 note for what has
> landed and what has not. Proposed 2026-08-03. Owner: TBD.
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

> **Re-verified 2026-09-05: G0 is complete and G1 is landing.** Wave 1 of #1113
> closed G0's loader tail, built `cmd/compatgen`, and turned up one new
> prerequisite for G2 — so the paragraph above naming `cmd/compatgen`,
> `compat/model/` and the loader tail as outstanding is superseded. State of the
> wave at this commit:
>
> | Deliverable | Merged | Open |
> | --- | --- | --- |
> | **G0 tail** (#1393, closed) — the seven suite loaders and `compat/mcp.go` read `registry.generated.json` | java-sdk #1680, node-js-sdk #1683, python-sdk #1682, go-sdk #1685 (carries `compat/mcp.go`), cli #1687, rust-sdk #1694, dotnet-sdk #1697 | — |
> | **G1** (#1394) — `sqs` added to the pruned shape snapshot; `cmd/compatgen` and `compat/model/` | #1684 | #1709 |
> | **G2 prerequisite** (#1700, closed) — every impl key qualified `group:test` | dotnet-sdk #1725, go-sdk #1711, python-sdk #1712, java-sdk #1713, rust-sdk #1714, cli #1715, docs #1716 | — |
>
> **The loader contract, decided once so all eight loaders agree.** A
> missing generated file loads as empty. A present but malformed one — bad JSON,
> an unsupported `version`, a group missing `generated`/`state`/`suites`, or a
> group name a hand-written group already owns — is a load error. Concatenation
> is hand-written first, generated after. Every loader gained an optional
> **scenario backend** hook, consulted for any test with no static impl,
> hand-written or generated, so the G6 port of a hand-written group to an
> authored scenario needs no loader change. Until the G2 interpreters exist, a
> generated test scoped to a suite with neither an impl nor a backend is a
> **`fail`** carrying exactly `generated group "<group>" is scoped to <suite>
> but <suite> has no scenario backend` — never `skip` and never `na`, because
> `suites` is derived from backend availability (§3.6), so a suite that cannot
> run a group it is named in is a generator or loader bug. `candidate` state
> keeps that out of both gates until promotion.
>
> **Deviation to record: `suites` scoping is not yet uniform.** `go-sdk`, `cli`,
> `python-sdk` and `node-js-sdk` honour `suites` for *every* group — it replaced
> the `service == "cdk"` carve-out, and against today's registry the two are
> behaviour-identical. `java-sdk`, `dotnet-sdk` and `rust-sdk` honour it for
> **generated groups only**: all three load `cdk-lifecycle` and record its 35
> tests as `skip` in `compat/baseline/<suite>.json`, and the PR-time baseline
> lint rejects a removed expectation. Aligning them means re-seeding those three
> shards — "changing what CI measures means re-seeding, not comparing"
> ([compat/AGENTS.md § Baseline & uniformity](../../compat/AGENTS.md#baseline--uniformity-policy))
> — which is a change of its own, now tracked as **#1737**.
>
> **The new G2 prerequisite: #1700, qualify every impl key — done.** Six suites
> registered bare `"<test>"` keys — `go-sdk` 487, `cli` 513, `python-sdk` 487,
> `java-sdk` 487, `dotnet-sdk` 208, `rust-sdk` 170; `node-js-sdk` already
> qualified everything by construction. Every loader refuses a bare key the
> moment two groups declare that name, so the first generated SQS group —
> `CreateQueue`, `SendMessage` and the rest beside `sqs-queues` and
> `sqs-messages`, because a generated test's name is the PascalCase operation
> name (§3.3) — would have aborted six suites at startup. Each rewrite was
> mechanical and proved binding-identical, and each suite gained a registration
> test that refuses a bare key. There is deliberately **no** registry-side
> ambiguity lint: a shared test name is normal, and a lint against it would fail
> on the generator's own naming convention at every model refresh, against
> §3.11's zero-human-actions rule — a first revision of #1716 built one and
> removed it for exactly that reason. The mechanical pass also found two latent
> registration faults, neither of which changed a binding: `python-sdk`'s
> `GetSendQuota` sat under the `ses-identities` section comment while the
> registry's only owner is `ses-send`, and `cli`'s `lambda.go` carried two dead
> bare keys duplicating qualified entries for the same two tests.
>
> **Recomputed at this commit**, from the checked-in artifacts:
> `compat/suites/registry.json` is **141 groups / 796 tests / 36 services**;
> `compat/baseline/` holds **5,467 entries** — 3,281 `pass`, 2,149 `skip`, 36
> `unimplemented`, 1 `na`, **0 `fail`** — with `dotnet-sdk.json` the largest
> shard at 128,996 B of the 512 KiB ceiling; `compat/parity-debt.json` holds
> **327** entries; and `compat/suites/registry.generated.json` is still
> `groups: []`, which is what keeps G0's empty-file gate meaningful.

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
([registry.ts](../../compat/suites/node-js-sdk/src/lib/registry.ts),
[registry.py](../../compat/suites/python-sdk/lib/registry.py)). **The qualified
form is now the only one anyone writes**: #1700 rewrote the six suites that
still registered bare keys and gave each a registration test that refuses one
(all seven of its PRs have merged and the issue is closed). The
bare fallback survives as a second line of defence, and is itself refused for a
name more than one group declares — which a generated group produces routinely,
since a generated test's name is the PascalCase operation name (§3.3). **A
generic scenario interpreter is one extra fallback
in that same lookup** — after the hand-written impl, before the
not-implemented sentinel. That hook landed with the G0 tail (#1393), so no
suite architecture changes.

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

> **The shipped vocabulary is wider than this sketch (#1709, 2026-09-05).**
> `compat/model/recipe.schema.json` is the authority; the sketch above is the
> shape, not the field list. What the pilot needed and the schema now carries:
> `operations` (authored coverage in the IR's own assertion vocabulary, for
> operations the lifecycle roles do not reach — `PurgeQueue`, the batch calls);
> `read.consuming` for a read that changes state (`ReceiveMessage`);
> `read.exports`; a plural `reads`; `setupOnly` for a resource that exists only
> to be required (the DLQ); a resource with no `create` at all, for one that
> pre-exists (`DescribeOrganization`); `async`, which wraps in `eventually`
> every clause that verifies by calling the service again — the derived
> read-back, list-membership and absence clauses, and authored clauses too,
> leaving alone a clause that only re-reads the test's own response and an
> authored `eventually` whose budget its author already chose; `tags`;
> `mutable.op` and `mutable.readPath`; `create.assert`; and **`neverProbe`**
> (§3.5). Timestamp, blob and document literals are refused by design — there is
> no portable literal for them — though no operation in either pilot service
> reaches that refusal today. The emitter never produces an `errorCode` clause
> of its own; a recipe may author one, and neither pilot service does.
>
> **Authored coverage is held to the guards, not exempted from them.** Only a
> clause that makes a call of its own — a `readback`, a `listContains` or
> `absent` carrying its own `call`, or an `eventually` around one — counts as
> verifying anything. So an authored `create.assert` built only of
> `responseField` clauses does not satisfy the create's read-back requirement
> (`no-readback-path`), and an authored update-family operation (`Update*`,
> `Set*`, `Put*`, `Tag*`, `Untag*` — one classifier, shared with the derived
> path) whose clauses all read its own response is refused
> (`update-without-readback`), which is guard 3 applied to `operations`.
>
> **One §3.5 lint is still unwritten.** "`$name` used for every user-supplied
> identifier" is enforced by recipe review today, not by the generator. It has
> the material to check it — `namesIn` already collects every `$name` suffix,
> and refuses two resources in a group claiming the same one — but nothing
> objects to a bare string literal where a `$name` belonged. Add it before G4
> puts recipe authoring on a per-service cadence.

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
4. a constraint-derived synthetic value (first enum member; range minimum;
   `false` for a required boolean) — only for scalars;
5. otherwise **refuse**. The operation is not generated and appears in
   `compat/model/gaps.json` with a machine-readable reason
   (`unbound-required-member:RoleArn`).

Optional members are left unset except the single `mutable` member on Update
operations. Refusal is the default and is cheap to fix (one line in a recipe);
guessing is never allowed.

> **What rule 4 shipped as (#1709).** "First enum member" is the order the
> shape snapshot carries — which is the model's own order for a
> `smithy.api#enum` trait, a JSON array, but *not* for a `type: enum` shape,
> whose members are a JSON object that `cmd/awsmodelgen` writes through
> `encoding/json` and therefore sorted. Every enum in the committed snapshots
> is of that second form today, so the pick is in fact the alphabetically first
> value; recovering declaration order means teaching `cmd/awsmodelgen` to emit
> an ordered member list. Either way the pick is deterministic, which is what
> byte-identical regeneration needs. A required boolean is synthesised as
> **`false`** — the shape has exactly two legal values and `false` is the one
> asking the service to do less (no dry run, no force, no cascade), so the
> choice is exhaustive rather than a guess. The fourth candidate above,
> "shortest legal string for a pattern", is deliberately **not** implemented: a
> pattern constrains a string's *syntax*, never its *reference*, so the
> shortest match for `^arn:aws:.*` is a well-formed ARN of something that does
> not exist. The emulator accepts far more of those than AWS does, which is
> exactly the class of value §3.10 sends to the gap report — so the member is
> refused and a human writes the literal.

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
| `isList` (#1709) | a `checks` entry rather than a clause of its own: a `List*` whose only assertable output is its page — the path resolves to a list, **empty or not**, and a member omitted rather than serialized as `[]` counts too | "observable state is verified", where the state is that the service answered with a page: an empty page is a legal single-page answer, so `nonEmpty` on a list the test did not populate is false by construction |

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
5. **A probe may not touch anything a run — or a real account — owns** (#1709).
   A probe is the one generated call no create/delete pair contains, so it has
   two guards of its own. (a) Binding rules 1 and 2 (§3.3) are switched off
   inside a probe group: it binds only curated `values.json` literals and
   constraint-derived ones, syntactically valid and deliberately nonexistent, so
   the call misses rather than lands. A member only a live export could supply
   refuses the operation (`probe-binds-live-resource:<Member>`). (b) A recipe's
   **`neverProbe`** map names each operation that is irreversible even against a
   stranger's identifiers, or that takes no identifier that could miss, with a
   curated sentence saying what it does that cannot be undone; the generator
   refuses those before binding them (`never-probe`), and the sentence is what
   `gaps.json` reports.
6. **No assertion a probe cannot honestly make** (#1709). A pagination token is
   never chosen as the identity — the member `@paginated` names as its
   `outputToken`, or any member named `NextToken`/`Marker`/`NextMarker`/
   `ContinuationToken`/`NextContinuationToken`/`PaginationToken` or ending in
   `Token` or `Marker` — because that is precisely the field AWS omits on a
   single-page answer, so asserting it non-empty asserts the opposite of a
   correct response. A `List*` left with only its page gets `isList` on that
   page instead (§3.4); an operation with neither an identity member nor a
   single list is refused (`no-output-to-assert`).

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
- **A scenario file has to be safe to point at AWS.** That is the same
  requirement read one step further: a probe "calls the operation once with
  model-valid literals" (§3.5), and against a real account that single call is
  `CloseAccount`, `DeleteOrganization` or `LeaveOrganization` — irreversible,
  and irreversible for the whole organization. So probe safety is not review
  advice but a structural guard, and it lands with the data rather than with the
  interpreters: §3.5's guard 5, in its two halves — a probe may never bind a
  value exported from a live resource, and a recipe's `neverProbe` names what
  must not be probed at all. `organizations` exercises both, and the result is a
  probe group that is entirely reads (§4.2).
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

> **Generated 2026-09-05 (#1709), ahead of the interpreters.** `compat/model/scenarios/sqs.json`
> covers **21 of the 23 modeled operations** in **23 tests** across four groups:
> `sqs-gen-queue` (13), `sqs-gen-message` (4), **`sqs-gen-batch`** (4) and
> `sqs-gen-probe` (2 — `CancelMessageMoveTask`, `ListMessageMoveTasks`). Inputs
> are one reviewed `recipes/sqs.json` and six curated `values.json` literals in
> eleven lines, inside criterion 1's fifteen-line budget, and `gaps.json`
> records **two** refusals for the service.
>
> Criteria 1 and 2 are met on the artifacts — 21 clears criterion 2's "≥ 20 of
> 23" — as is criterion 3's structural half: the IR cannot express a test
> without an assertion clause. Everything that needs a run — 4, 5 and 6 — waits
> for a backend and stays open for G2.
>
> **Criterion 3's second half no longer holds as written, and should be
> restated.** The two `StatusUnsupported` operations do *not* land in
> `sqs-gen-probe` recording `unimplemented`: `AddPermission` and
> `RemovePermission` both return an empty output, and reading back the queue
> they name would assert something that was already true before the call, so
> both are refused `no-output-to-assert` into `gaps.json`. That is the §3.4
> invariant — no assertion, no test — applied honestly rather than worked
> around, and it is a better result than a probe that asserts nothing. Read the
> criterion as: *every generated test has ≥ 1 assertion clause, and every
> `StatusUnsupported` operation either lands in `sqs-gen-probe` and records
> `unimplemented` or appears in `gaps.json` with a specific reason.* The probe
> group's remaining two operations — `CancelMessageMoveTask` and
> `ListMessageMoveTasks`, modeled but undeclared — are the ones that carry the
> `unimplemented` demonstration.
>
> The fourth group is the one departure from the criteria as written: batch
> operations fit none of the lifecycle roles, so they are authored as a
> `sqs-gen-batch` resource of their own rather than refused. The plan asked for
> two groups; four is what the service's shape produces.

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

> **Generated 2026-09-05 (#1709), and here is the recount.** Of the 63 modeled
> operations, all nine `StatusInert` ones are covered by lifecycle groups —
> `organizations-gen-policy` (8 tests: the full
> Create → Describe → Update → Tag/ListTags/Untag → List → Delete lifecycle) and
> `organizations-gen-organization` (1: `DescribeOrganization`, asserting
> `$.Organization.Arn` against the model's own pattern). That leaves **54**
> undeclared operations, of which `organizations-gen-probe` covers **25** — so
> **34 of 63** operations covered, by 34 tests in three groups. Criterion 1's
> "62" reads "25" today.
>
> The other **29** are refusals, every one of them `never-probe`, and every one
> carrying a curated sentence in `gaps.json` saying what the call does that
> cannot be undone. They are the whole of §3.5's guard 5 landing with the data:
> `recipes/organizations.json` gained a `neverProbe` map listing every modeled
> operation that writes — the account-mutating ones (`CloseAccount`,
> `CreateAccount`, `CreateGovCloudAccount`, `RemoveAccountFromOrganization`,
> `MoveAccount`), the organization-lifecycle ones (`CreateOrganization`,
> `DeleteOrganization`, `LeaveOrganization`, `EnableAllFeatures`), the
> policy-attachment and policy-type toggles (`AttachPolicy`, `DetachPolicy`,
> `EnablePolicyType`, `DisablePolicyType`), the service-access toggles
> (`EnableAWSServiceAccess`, `DisableAWSServiceAccess`), the handshake and
> invitation calls (`AcceptHandshake`, `CancelHandshake`, `DeclineHandshake`,
> `InviteAccountToOrganization`), the delegated-administrator pair
> (`RegisterDelegatedAdministrator`, `DeregisterDelegatedAdministrator`), the
> resource-policy writes (`PutResourcePolicy`, `DeleteResourcePolicy`), the
> responsibility-transfer calls
> (`InviteOrganizationToTransferResponsibility`, `UpdateResponsibilityTransfer`,
> `TerminateResponsibilityTransfer`) and the organizational-unit writes
> (`CreateOrganizationalUnit`, `UpdateOrganizationalUnit`,
> `DeleteOrganizationalUnit`). What that buys is a probe group which is entirely
> reads — nothing in it could damage a real account, which is the condition
> §3.10 puts on pointing a scenario file at AWS.
>
> This **subsumes the earlier refusals** an interim revision of #1709 reported:
> the six `no-output-to-assert` ones and the
> `unbound-required-member:StartTimestamp` on
> `InviteOrganizationToTransferResponsibility` are all in the `neverProbe` list
> now, and are refused earlier, before anything is bound. `gaps.json` for
> `organizations` is 29 `never-probe` entries and nothing else.
>
> Criterion 3 no longer holds for the service as a whole: the policy lifecycle
> creates and deletes a real resource, so `organizations-gen-policy` has a
> teardown. It still holds for `organizations-gen-organization`, which is the
> group it was written about — and, now, for `organizations-gen-probe`, which
> carries neither setup nor teardown because a probe has nothing to set up.

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

### 4.4 G2 handoff — what an interpreter author has to agree to

Added 2026-09-05 from #1709's own report. The IR and its contract are settled
and documented for interpreter authors in
[compat/model/README.md](../../compat/model/README.md) (new in #1709); what
follows is the set of decisions three interpreters have to make identically,
and the places the pilot is expected to bite.

**The contract, in one list.**

- **Error matching.** An `error` clause carries both the modeled `shape` and the
  wire `code` — for SQS's not-found, `QueueDoesNotExist` and
  `AWS.SimpleQueueService.NonExistentQueue` — and an interpreter accepts an
  error whose reported code **or** type name equals **either**, because the
  SDKs disagree about which of the two they surface. Overcast puts the legacy
  code in the JSON `__type` and sends no `x-amzn-query-error` header
  ([internal/services/sqs/store.go](../../internal/services/sqs/store.go));
  AWS's JSON-protocol SQS is understood to carry it in that header instead, so
  confirm which one each SDK reports during the soak rather than assuming.
- **Names.** `$name` is `{OVERCAST_COMPAT_RUN_ID}-{group}-{suffix}`, with the
  group token the *whole* group name and no shortening anywhere — that is what
  makes the name-hygiene rule (§2.4) hold by construction.
- **`eventually`.** Exports from a `readback` inside it are applied on the
  attempt that passes, and only then.
- **Setup and teardown.** A setup failure — an error or an unresolvable `$ref` —
  reports **every** test in the group as `skip` with `setup failed: <error>`,
  and teardown still runs. Each teardown step is wrapped individually: one
  failure skips that step and the rest continue.
- **`equals`** is JSON equality *after* the SDK's own mapping, never string
  comparison; timestamps and blobs are never compared.
- **`isList`** holds when the path resolves to a list, empty or not — and when
  it does not resolve at all, because several AWS services omit an empty list
  member instead of serializing `[]` (SQS's `ListQueues` among them). A present
  value that is *not* a list still fails it. It is the check every `List*` probe
  carries, so getting it wrong fails 16 of the 25 `organizations` probes at
  once, and `nonEmpty` never substitutes for it.
- **Probe safety is the interpreter's rule too.** A probe group has no setup and
  no teardown, and every value it sends is a curated or synthetic literal that
  names nothing the run owns (§3.5 guard 5). An interpreter must not "helpfully"
  fill a missing member from context, retry a probe against a different
  identifier, or clean up after one — there is nothing to clean up, and a probe
  that reaches a real resource is the one failure mode a scenario file pointed
  at AWS cannot recover from.
- **Failure messages** carry, in order, the six fields listed in the README's
  § Failure messages: `group/test`, the operation, the exact params JSON sent,
  the assertion kind and path, expected versus actual, and the scenario file
  plus step index. `cmd/compatgen -explain <group>/<test> -lang <language>`
  renders the same test as pseudo-code so a failure reproduces by hand.
- **Landing a backend** means flipping that one suite in `scenarioBackends` in
  [cmd/compatgen/registry.go](../../cmd/compatgen/registry.go) in the
  interpreter's own PR, and regenerating. The table is per suite, so each of the
  three PRs flips its own entry and commits the regenerated
  `registry.generated.json`; until one suite is in it the generated registry
  stays `groups: []` by construction (§3.6), so the interpreter and the groups
  it can run arrive together.

**Where this list is coordinated.** G2 is tracked as **#1768**, which carries
this contract, the one-PR-per-suite breakdown (`python-sdk`, `node-js-sdk`,
`cli`) and the §4.1/§4.2 acceptance criteria as its definition of done. The
normative spec the three interpreters are written against is
`compat/model/README.md`; this section is the set of decisions they have to make
identically, not a second copy of it. Take an open question here to #1768 rather
than settling it in one suite.

**Fidelity assumptions to watch in the soak.** Each is a deliberate choice
recorded in `recipes/sqs.json`, and each is a plausible source of a first-run
surprise: the batch group tracks whichever entry arrives first, via
`Messages[0]`; the queue resource's `async` budget is 30 attempts 2 s apart, so
`DeleteQueue`'s absence check and the `ApproximateNumberOfMessages` read-back
each get the full minute AWS documents for those rather than the five seconds a
queue read-back would otherwise take; the `PurgeQueue` read-back allows a
minute of its own (12 attempts, 5 s apart) because AWS documents the counters
as lagging; `DeleteMessageBatch` quotes the receipt handle the `ReceiveMessage`
list test re-exports, because AWS asks a delete to carry the most recent one;
and every `ReceiveMessage` that must leave a message visible passes
`VisibilityTimeout: 0`, while the two that must leave it in flight do not,
because `ChangeMessageVisibility` on a visible message is `MessageNotInflight`
on AWS.

**One assertion was already known to fail, and it has since been fixed.**
Identity fields are asserted against the model's own pattern where RE2 can
express it. `organizations` models an organization ARN as
`^arn:aws:organizations::\d{12}:organization\/o-[a-z0-9]{10,32}$`, and the inert
implementation used to mint the organization as `o-overcast` — eight characters
after the `o-` where AWS requires ten to thirty-two — so
`organizations-gen-organization/DescribeOrganization` (§4.2's criterion 2) and
the ARN check in `organizations-gen-policy/CreatePolicy`, whose ARN embeds the
same id, both failed against Overcast. That was the generator doing its job: a
model-derived assertion catching an identifier a hand-written test would have
been written around. It was filed as **#1736** and fixed by **#1750**, which
derives the id deterministically from the account id as `o-` plus ten hex
characters
([internal/services/organizations/inert_policy.go](../../internal/services/organizations/inert_policy.go),
with `aws_id_pattern_test.go` holding the pattern). Both assertions should now
pass; the G2 run is what proves it.

---

## 5. Phasing

Status as of 2026-09-05 is in the §2 note: **G0 is done, G1 is landing, and G2
is unblocked once #1709 lands** — #1700 has merged in full. The `Status` column
below records that; `Contents` is left as written so the original scope stays
legible.

| Phase | Status | Contents | Effort | Acceptance gate |
| --- | --- | --- | --- | --- |
| **G0** Foundations | **Done** — #1356, #1357, #1367, #1370, and the loader tail under #1393, all seven suite PRs merged and the issue closed. One deviation: `suites` scoping is honoured for every group in four suites and for generated groups only in `java-sdk`, `dotnet-sdk` and `rust-sdk`, pending the baseline re-seed tracked as #1737 — see the §2 note | Shard `compat/baseline.json` → `compat/baseline/<suite>.json` (+ size budget); `--shard i/n` and `--generated-registry-file` in `cmd/compat`; `registry.generated.schema.json`; all 8 loaders read the generated sibling and fall back to a scenario resolver hook; `candidate`/`gated` state honoured by both gates; `compat/AGENTS.md` amendment for generated `suites` scoping + the lint that bounds it | M | With an **empty** generated registry, every gate, report and dashboard behaves exactly as today; baseline shards aggregate byte-identically; the scoping lint rejects a hand-written group that adds `suites` |
| **G1** Model layer | **Done**, pending #1709's merge — `internal/awsmodel` #1359, shape snapshot via inert-tier I1 with `sqs` added in #1684, `cmd/compatgen` and `compat/model/` in #1709 | Extract `internal/awsmodel` AST reader; `cmd/compatgen` skeleton; the pruned shape snapshot `models/aws/shapes/` + `shapes-sha256` (shared deliverable with [inert-tier-rollout.md](./inert-tier-rollout.md) Phase I1 — build once, whichever plan gets there first); IR + recipe JSON schemas; `--scaffold`, `--review-report`, `--explain`; `gaps.json` | M | `make compat-model-check` regenerates byte-identically offline; the sha gate catches a hand edit; the snapshot is within its size budget; scaffolding a service produces a recipe skeleton a human can complete |
| **G2** Pilot | Not started, tracked as **#1768** — **gated on #1709** alone now that #1700 and #1750 have merged. Both recipes and both scenario files already exist (#1709); what is missing is the three interpreters, one PR each. Start from §4.4 | `python-sdk`, `node-js-sdk` and `cli` interpreters; `recipes/sqs.json` + `recipes/organizations.json`; the §4 acceptance criteria | L | Every §4.1 and §4.2 criterion met, including the regeneration demonstration in §4.2.5 |
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

1. **`shapes.json` size budget and contents — resolved 2026-09-05, measured.**
   No depth limit is needed at this scope, and the fallback of deriving shapes
   at generation time is not required. On the five committed services the
   snapshot is **307,497 B of the 336 KiB budget** — `batch` 102,309,
   `organizations` 96,119, `servicediscovery` 39,316,
   `elastic-load-balancing` 35,614, `sqs` 34,139 — which is roughly 1.2–2.3 KB
   per operation. Maximum reference depth is 6–7 for four of the five and
   **16** for `batch`. There is exactly one recursive shape in the set
   (`organizations`'s `HandshakeResource.Resources` → `HandshakeResources` →
   `HandshakeResource`), and the pruner terminates on it by taking the closure
   rather than by bounding depth, so recursion costs nothing to allow.
   Re-measure when the allowlist widens: the budget, not a depth cap, is the
   thing that has to hold.
2. **The `suites`-scoping amendment** to `compat/AGENTS.md` (§3.6) changes a rule
   that currently reads as absolute. It needs explicit reviewer agreement before
   G0 lands, not after.
3. **One name-mapping table, four consumers — decided 2026-09-05: there is no
   table.** The scenario header carries `sdkId`, `endpointPrefix`,
   `signingName`, `protocol`, `apiVersion` and `targetPrefix` and nothing
   SDK-specific; each backend derives its own package or class name from those,
   by the per-backend rules in
   [compat/model/README.md § Naming](../../compat/model/README.md). A table
   would need seven columns maintained by hand for every service the allowlist
   gains, which is the enumeration §3.11 exists to avoid.
   **The residual is a short list of known derivation breaks**, recorded in that
   section as per-backend follow-ups rather than smuggled into the IR: botocore's
   service name differs from the endpoint prefix for `elasticloadbalancing`
   → `elb`, `monitoring` → `cloudwatch`, `email` → `ses` and `states` →
   `stepfunctions`; and the Go SDK package is `sfn` for `SFN` and
   `elasticloadbalancing` for `ELB`, neither derivable from the SDK id. Neither
   pilot service breaks. Drift detection is still unbuilt: the first backend
   that needs an override should land the override table and a test that the
   client it names actually constructs.
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
