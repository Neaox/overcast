# AGENTS.md — rust-sdk suite

> Conventions for AI agents and contributors working in
> `compat/suites/rust-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules, the separation boundary, and the `group:test`
> implementation-key rules that apply to every suite.
> This file covers only rust-sdk-specific details.
>
> For quick-start, prerequisites, env vars and architecture see
> [README.md](README.md).

---

## What this suite tests

Core AWS service operations reachable via the **AWS SDK for Rust**. It is the
Rust column of the compatibility matrix. Failures on unimplemented services
are correct and expected — they are the coverage gap metric, not bugs to
silence.

Unlike the suites that aim at full parity with `node-js-sdk`, this one covers
ten services on purpose. Every other registry test still runs and reports a
`skip`, so the narrower scope is visible in the results rather than hidden.

---

## Status

**Implemented.** `all_service_groups` (in `src/main.rs`) wires ten group
structs: S3, SQS, DynamoDB, SNS, Lambda, STS, KMS, Secrets Manager, SSM and
EventBridge. It runs in the compat CI matrix
(`.github/workflows/compat.yml`), where a dedicated `rust-sdk-image` job
builds and publishes its image.

Widening coverage means adding a group struct and its `aws-sdk-*` crate — not
building the suite from scratch. Start from the code that is here.

---

## Runtime

| Item       | Value                                                                                          |
| ---------- | ------------------------------------------------------------------------------------------------ |
| Language   | Rust, edition 2021, async on Tokio                                                              |
| AWS client | `aws-sdk-*` crates pinned exactly (`=1.x.y` in `Cargo.toml`), one per service                    |
| Errors     | `Result<(), String>` throughout — **no `anyhow`**; SDK errors go through `harness::sdk_error`     |
| CI image   | `rust:1.94.1-slim-bookworm` build stage → `debian:bookworm-slim` runtime (see `Dockerfile`)      |
| Profile    | Debug, not release. Test-harness build speed beats runtime speed here                            |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).
> `Cargo.toml` is the source of truth for every pinned version; do not restate
> one here. `Cargo.lock` is not checked in — the exact `=` pins stand in for it.

---

## File layout

```
compat/suites/rust-sdk/
  AGENTS.md        ← you are here
  README.md        ← quick-start, prerequisites, env vars, architecture
  Cargo.toml       ← one pinned aws-sdk-* crate per service, plus tokio and serde
  Dockerfile       ← build stage runs `cargo test` before `cargo build`
  run.sh           ← resolves/builds the image and runs it; cmd/compat invokes it
  image-tag.sh     ← content hash of the Rust sources and both registry files
  BUILD_NOTES.md   ← the BuildKit cache-mount notes behind the Dockerfile

  src/
    main.rs        ← entry point and the registration test
    clients.rs     ← AwsClients: one constructor per service over a shared SdkConfig
    harness.rs     ← TestContext, TestCase, TestGroup, run_suite, sdk_error, NDJSON
    registry.rs    ← registry loading, impl-key merge/validate, group building,
                     and the loader unit tests
    groups/
      mod.rs       ← the ServiceGroup trait and the module list
      s3.rs  sqs.rs  dynamodb.rs  sns.rs  lambda.rs
      sts.rs  kms.rs  secretsmanager.rs  ssm.rs  eventbridge.rs
```

**One module per AWS service.** Never split a service across modules, and
never merge two services into one.

---

## Group anatomy

A service module exposes one struct implementing `ServiceGroup`: three maps
(`impls`, `setups`, `teardowns`) that `main` merges into the suite's
registrations at startup. Each impl is an `Arc`'d closure that clones the
`Arc<AwsClients>` it captures and returns a boxed future. This is the real
`EventBridgeGroup` (`src/groups/eventbridge.rs`), trimmed:

```rust
pub struct EventBridgeGroup {
    clients: Arc<AwsClients>,
}

impl EventBridgeGroup {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for EventBridgeGroup {
    fn name(&self) -> &'static str {
        "eventbridge"
    }

    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();

        let clients = self.clients.clone();
        impls.insert(
            "eventbridge-patterns:TestEventPattern".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .eventbridge()
                        .test_event_pattern()
                        .event_pattern(pattern)
                        .event(sample_event(&ctx))
                        .send()
                        .await
                        .map_err(crate::harness::sdk_error)?;
                    if !response.result() {
                        return Err(format!(
                            "TestEventPattern: expected Result=true for pattern {pattern}"
                        ));
                    }
                    Ok(())
                })
            }),
        );

        impls
    }

    fn setups(&self) -> HashMap<String, TestFn> { HashMap::new() }
    fn teardowns(&self) -> HashMap<String, TestFn> { HashMap::new() }
}
```

Setup and teardown are `TestFn` values too, keyed by **group name** rather
than `group:test`. Clients come from `clients.s3()`, `clients.sqs()` and so
on — never build an SDK client inside a closure.

`name()` labels the module a duplicate-key error came from, so a collision
points at the two files rather than only the key they disagree about.

---

## Key types

```rust
// src/harness.rs
pub type TestFuture = Pin<Box<dyn Future<Output = Result<(), String>> + Send>>;
pub type TestFn = Arc<dyn Fn(TestContext) -> TestFuture + Send + Sync>;

#[derive(Clone)]
pub struct TestContext {
    pub endpoint: Arc<String>,
    pub region: Arc<String>,
    pub run_id: Arc<String>,
    // plus a Mutex'd HashMap<String, String> reached through set/get
}

impl TestContext {
    pub fn set(&self, key: &str, value: String);
    pub fn get(&self, key: &str) -> Option<String>;
    pub fn log(&self, msg: &str);   // stderr only — stdout is NDJSON
}

pub struct TestCase {
    pub name: String,
    pub op: Option<String>,      // AWS operation name for doc links
    pub skip: Option<String>,    // Some(reason) emits "skip" without running
    pub depends: Vec<String>,    // same-group tests that must pass first
    pub fn_: TestFn,
}

pub struct TestGroup {
    pub suite: String,
    pub service: String,
    pub name: String,
    pub tests: Vec<TestCase>,
    pub setup: Option<TestFn>,
    pub teardown: Option<TestFn>,
}

/// Renders an SdkError with its whole source() chain. `SdkError`'s own
/// Display is the single word "service error".
pub fn sdk_error(err: impl std::error::Error + Send + Sync + 'static) -> String;
```

**The state bag holds `String` values only.** Anything else — a number, a
struct — is serialised on the way in and parsed on the way out.

---

## Naming conventions

| Element         | Convention                                                                                                                                                                 |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Impl map key    | `<group>:<Test>` — **always group-qualified**, never bare (see [compat/AGENTS.md § Implementation keys](../../AGENTS.md#implementation-keys--grouptest-and-a-bad-key-aborts-the-run)) |
| Setup/teardown key | The bare group name, e.g. `"s3-crud"`                                                                                                                                     |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `sqs-fifo`                                                                                                              |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `SendMessage`                                                                                                          |
| Resource prefix | `{ctx.run_id}-<short>`, e.g. `{run_id}-s3crud`                                                                                                                             |
| Module          | Lowercase service name: `s3`, `sqs`, `dynamodb`                                                                                                                            |
| Struct          | `<Service>Group`, e.g. `S3Group`, `DynamoDbGroup`                                                                                                                          |
| Context key     | snake_case string, e.g. `"bucket"`, `"queue_url"`                                                                                                                          |

---

## Inter-test state

`TestContext` is cloned per group and its bag is shared through an `Arc<Mutex<…>>`,
so a value set by one test is visible to the next one in the same group:

```rust
// in setup, or an earlier test:
ctx.set("queue_url", url.to_string());

// in a later test, or in teardown:
let url = ctx
    .get("queue_url")
    .ok_or_else(|| "queue_url not set".to_string())?;
```

Never rely on inter-group state, and never stash an SDK client — it already
lives in `AwsClients`.

---

## Teardown rules (Rust-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Rust specifics:

- Ignore teardown errors with `let _ = …` or `.ok()` — never propagate them.
  One failed delete must not abort the deletes after it.
- `ctx.get("key")` returns `None` rather than panicking when setup failed
  before setting the value; return early instead of unwrapping.
- For S3, delete every object (and every version, when the bucket is
  versioned) before `delete_bucket`.
- Map every SDK error through `crate::harness::sdk_error` on the way to a
  `String`. `SdkError`'s own `Display` is the single word "service error", and
  reporting only that is what once made nine baseline failures unactionable.
- AWS SDK Rust errors implement `ProvideErrorMetadata`, so an unimplemented
  operation can be recognised by `err.code() == Some("NotImplemented")` or an
  HTTP 501 in the raw response.

---

## Error messages

A test fails by returning `Err(String)`. Lead with the AWS operation name and
include what was expected, so the NDJSON `error` field is readable in the
dashboard without opening the source:

```rust
return Err(format!(
    "ListObjectsV2: key {key} not found in bucket {bucket} (run_id={})",
    ctx.run_id
));
```

---

## Generated groups and the `ScenarioBackend` hook

`src/registry.rs` loads `registry.json` and its machine-written sibling
`registry.generated.json` (concatenated, hand-written groups first — see
[compat/AGENTS.md § registry.json](../../AGENTS.md#registryjson--canonical-test-matrix)),
validates this suite's impl keys against the result, and builds the groups the
harness runs.

A **generated** group is meant to be executed by an interpreter reading the
group's scenario IR rather than by a registered impl. `ScenarioBackend` is the
extension point for that interpreter, consulted after the qualified and bare
impl-key lookups and before the not-implemented sentinel.

**Nothing implements it in this suite yet** — `main` passes `None` — and
`registry.generated.json` currently declares no groups, so this has no
observable effect today. Once the generator emits a group scoped to
`rust-sdk`, a generated test with no backend reports as a hard **failure**
naming the group, not a skip. Do not add a backend speculatively; wait until
there is a real interpreter to wire in.

---

## Registration tests

`src/main.rs` carries `registration_tests`, which merges every service's real
impl map through the same `merge_impls` the binary uses and resolves the
result against the real `registry.json`:

- merging the real maps **is** the duplicate-key check;
- validating them against the real registry **is** the bare-key check.

The fixture-based loader tests in `src/registry.rs` cover merge, validate,
dependency ordering and `registry.generated.json` handling on synthetic
registries — which cannot see a collision introduced in an actual service
file, hence the pair.

The Dockerfile runs `cargo test` in its build stage, so both sets run on every
image build and a broken registration fails it rather than quietly changing
what a run reports. Building the image is therefore the way to run them
without a host Rust toolchain:

```bash
# from the repo root; the branch-named tag keeps parallel worktrees apart
docker build -f compat/suites/rust-sdk/Dockerfile \
  -t "oc-rust-sdk-compat:$(sh scripts/image-tag.sh)" compat/suites
```

---

## Adding a new group

1. Add the group and its tests to [compat/suites/registry.json](../registry.json)
   first (see [compat/AGENTS.md § Uniformity](../../AGENTS.md#2-uniformity--the-registry-is-the-contract)),
   or confirm another suite has already declared them.
2. Create or open `src/groups/<service>.rs` and implement `ServiceGroup`.
3. Register every test under a **group-qualified** key in `impls()`, and its
   setup/teardown under the bare group name.
4. For a new service, add the module to `src/groups/mod.rs`, the struct to
   `all_service_groups` in `src/main.rs`, a constructor to `src/clients.rs`,
   and the pinned `aws-sdk-*` crate to `Cargo.toml`.
5. Build the image (or run `cargo test`, with a host toolchain) — the
   registration test fails on a mis-keyed registration without needing a run.
6. Run the suite against a live instance (see
   [README.md § Running the suite](README.md#running-the-suite)) and check the
   NDJSON output.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree
  — see [compat/AGENTS.md § Separation boundary](../../AGENTS.md#separation-boundary--non-negotiable).
- Never hard-code the endpoint — clients are configured once from
  `OVERCAST_ENDPOINT` in `AwsClients::new`.
- Never use `std::thread::sleep`, or `tokio::time::sleep` with a fixed
  duration, inside a test — poll with a maximum retry count instead.
- Never construct SDK clients inside a test closure — clone the
  `Arc<AwsClients>` the group holds.
- Never register an impl key without the `group:` qualifier — the registration
  test rejects it, and the Docker build fails with it.
- Never add a setup entry without a corresponding teardown entry.
- Never call `delete_bucket` without first emptying the bucket.
- Never schedule KMS key deletion without first deleting its aliases.
- Never write to stdout inside a test — the runner parses stdout as NDJSON;
  use `ctx.log()` (stderr) for diagnostics.
- Never add `anyhow` (or another error crate) to `Cargo.toml` for test code —
  the harness's error type is `String`, and `sdk_error` is how an SDK error
  becomes one.
