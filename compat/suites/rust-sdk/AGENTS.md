# AGENTS.md — rust-sdk suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/rust-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers Rust SDK-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

Core AWS service operations reachable via the **AWS SDK for Rust**. It is the
Rust column of the compatibility matrix. Failures on unimplemented services
are correct and expected — they are the coverage gap metric, not bugs to
silence.

Planned coverage (core services only, not full parity with `node-js-sdk`):
**S3, SQS, DynamoDB, SNS, Lambda, STS, KMS, Secrets Manager, SSM**.

---

## Status

**Implemented.** Covers 9 AWS services: S3 (7 groups), SQS (4 groups), DynamoDB (6 groups), SNS (3 groups), Lambda (5 groups), STS (2 groups), KMS (3 groups), Secrets Manager (2 groups), SSM (3 groups).

---

## Runtime

| Item       | Value                                                |
| ---------- | ---------------------------------------------------- |
| Language   | Rust (stable toolchain, edition 2021)                |
| AWS client | `aws-sdk-*` crates (pinned exactly: `=1.x.y` in `Cargo.toml`) |
| CI image   | `rust:1.94.1-slim-bookworm`                                    |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout (planned)

```
compat/suites/rust-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← rust:1-alpine; builds release binary; runs it
  Cargo.toml         ← workspace or single crate; tokio, aws-sdk-* crates
  Cargo.lock

  src/
    main.rs          ← entry point; runs suite; emits NDJSON to stdout
    harness/
      mod.rs         ← TestContext, TestGroup, TestCase, run_suite()
      context.rs     ← TestContext struct; inter-test state bag
    clients/
      mod.rs         ← AwsClients struct; lazy-init client factory
    groups/
      mod.rs         ← registers all group modules
      s3.rs
      sqs.rs
      dynamodb.rs
      sns.rs
      lambda.rs
      sts.rs
      kms.rs
      secretsmanager.rs
      ssm.rs
```

**One file per AWS service.** Never split a single service group across files.

---

## Group anatomy

```rust
// groups/s3.rs
use crate::harness::{TestCase, TestContext, TestGroup};
use crate::clients::AwsClients;
use aws_sdk_s3::types::BucketLocationConstraint;
use std::sync::Arc;

pub async fn groups(suite: &str, clients: Arc<AwsClients>) -> Vec<TestGroup> {
    let c = clients.clone();
    vec![
        TestGroup {
            suite: suite.to_owned(),
            service: "s3".to_owned(),
            name: "s3-crud".to_owned(),
            tests: vec![
                TestCase::new("CreateBucket", {
                    let c = c.clone();
                    move |ctx| {
                        let c = c.clone();
                        Box::pin(async move {
                            let bucket = format!("{}-s3-crud", ctx.run_id);
                            c.s3()
                                .create_bucket()
                                .bucket(&bucket)
                                .send()
                                .await?;
                            ctx.set("bucket", bucket);
                            Ok(())
                        })
                    }
                }),
                TestCase::new("ListBuckets", {
                    let c = c.clone();
                    move |ctx| {
                        let c = c.clone();
                        Box::pin(async move {
                            let bucket = ctx.get::<String>("bucket")
                                .ok_or("bucket not set")?;
                            let resp = c.s3().list_buckets().send().await?;
                            let found = resp.buckets()
                                .iter()
                                .any(|b| b.name().unwrap_or_default() == bucket);
                            if !found {
                                anyhow::bail!(
                                    "bucket {} not found in ListBuckets (run_id={})",
                                    bucket, ctx.run_id
                                );
                            }
                            Ok(())
                        })
                    }
                }),
            ],
            setup: None, // bucket created in CreateBucket test above
            teardown: Some({
                let c = c.clone();
                Box::new(move |ctx| {
                    let c = c.clone();
                    Box::pin(async move {
                        if let Some(bucket) = ctx.get::<String>("bucket") {
                            let _ = c.s3()
                                .delete_bucket()
                                .bucket(&bucket)
                                .send()
                                .await;
                        }
                    })
                })
            }),
        },
    ]
}
```

---

## Key types

```rust
// harness/context.rs
use std::any::Any;
use std::collections::HashMap;

pub struct TestContext {
    pub endpoint: String,
    pub region:   String,
    pub run_id:   String,
    state: HashMap<String, Box<dyn Any + Send + Sync>>,
}

impl TestContext {
    pub fn set<T: Any + Send + Sync>(&mut self, key: &str, value: T) {
        self.state.insert(key.to_owned(), Box::new(value));
    }
    pub fn get<T: Any + Clone>(&self, key: &str) -> Option<T> {
        self.state.get(key)?.downcast_ref::<T>().cloned()
    }
    pub fn log(&self, msg: &str) {
        eprintln!("{}", msg); // stderr only
    }
}

// harness/mod.rs (sketch)
pub struct TestCase { pub name: String, pub fn_: TestFn }
pub struct TestGroup {
    pub suite:    String,
    pub service:  String,
    pub name:     String,
    pub tests:    Vec<TestCase>,
    pub setup:    Option<SetupFn>,
    pub teardown: Option<TeardownFn>,
}
```

Type aliases for async function pointers (`TestFn`, `SetupFn`, `TeardownFn`)
use `Pin<Box<dyn Future<Output = anyhow::Result<()>> + Send>>`. Use `anyhow`
for error handling across all test functions.

---

## Naming conventions

| Element         | Convention                                                        |
| --------------- | ----------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `sqs-basic`   |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `SendMessage` |
| Resource prefix | `{run_id}-<group-short>` e.g. `{run_id}-s3-crud`                  |
| Module          | Lowercase service name: `s3`, `sqs`, `dynamodb`                   |
| Context key     | snake_case string, e.g. `"bucket"`, `"queue_url"`                 |

---

## Inter-test state

Use `ctx.set`/`ctx.get::<T>` to pass data between sequential tests:

```rust
// set in an earlier test:
ctx.set("queue_url", url.to_string());

// retrieve in a later test:
let url = ctx.get::<String>("queue_url")
    .ok_or("queue_url not set")?;
```

Never rely on inter-group state. Never stash SDK client objects in the context.

---

## Teardown rules (Rust-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Rust specifics:

- Ignore teardown errors with `let _ = ...` or `.ok()` — never propagate them.
- Use `ctx.get::<T>("key")` in teardown — returns `None` (not a panic) when
  setup failed before setting the value.
- For S3, delete all objects (and versions if versioned) before calling
  `delete_bucket`.
- AWS SDK Rust errors implement `ProvideErrorMetadata`; check
  `err.code() == Some("NotImplemented")` or HTTP status 501 to detect
  unimplemented operations.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — configure clients with
  `endpoint_url(&ctx.endpoint)`.
- Never use `std::thread::sleep` or `tokio::time::sleep` with a fixed duration
  — use a poll loop with a max retry count.
- Never construct SDK clients inside test closures — inject them via
  `AwsClients` and clone the `Arc`.
- Never add a setup function without a corresponding teardown.
- Never call `delete_bucket` without first emptying the bucket.
- Never schedule KMS key deletion without first deleting aliases.
- Never write to stdout inside a test function — the runner parses stdout as
  NDJSON; use `ctx.log()` (stderr) for diagnostics.

---

## Implementation checklist

When building this suite from scratch:

1. Create `Cargo.toml` with the Tokio runtime (`tokio = { features = ["full"]
}`), `anyhow`, and `aws-sdk-*` crates for the nine planned services.
2. Implement `harness/context.rs` and `harness/mod.rs` with the types above.
3. Implement `clients/mod.rs` — each client configured with
   `endpoint_url()`, a fake region, and `Credentials::new("test", "test",
None, None, "test")`.
4. Implement group modules starting with `s3.rs`, mirroring `node-js-sdk`
   coverage for the nine planned services.
5. Register all groups in `groups/mod.rs` and wire them in `main.rs`.
6. Emit NDJSON to stdout from `main.rs` using `run_suite()`.
7. Create `Dockerfile`: use `rust:1-alpine`; run `cargo build --release`;
   set `CMD` to the compiled binary. Use a multi-stage build (builder +
   Debian/Alpine final image) to minimise image size.
8. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
9. Run `cargo clippy -- -D warnings` to confirm no lint errors.
10. Run the suite locally against a live Overcast instance and verify output.
