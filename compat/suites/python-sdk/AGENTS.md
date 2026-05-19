# AGENTS.md — python-sdk suite

> Conventions for AI agents and contributors working in `compat/suites/python-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers only python-sdk-specific details.

---

## What this suite tests

Every AWS service operation reachable via **boto3** (AWS SDK for Python v3).
It is the Python SDK column of the compatibility matrix. Failures on
unimplemented services are correct and expected.

---

## Runtime

| Item       | Value                |
| ---------- | -------------------- |
| Language   | Python 3.11          |
| AWS client | `boto3>=1.34.0` / `botocore>=1.34.0` (pinned)                   |
| CI image   | `python:3.11-alpine`                                              |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout

```
compat/suites/python-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start
  runner.py          ← entry point; imports all group modules; NDJSON output
  requirements.txt
  lib/
    harness.py       ← TestContext, run_suite()
    clients.py       ← make_clients(endpoint, region) → named tuple of clients
  groups/            ← one file per AWS service
    s3.py
    sqs.py
    ...
```

**One file per AWS service.** Never split a service across files.

---

## Group anatomy

Each service file must export these module-level dicts:

```python
IMPLS: dict[str, Callable[[TestContext], None]]
SETUP: dict[str, Callable[[TestContext], None]]   # keyed by group name
TEARDOWN: dict[str, Callable[[TestContext], None]] # keyed by group name
```

Individual test functions raise `AssertionError` to fail; return normally to
pass. They must not call `sys.exit`.

Context state is stored and read via `ctx["key"]` / `ctx.get("key")`.

---

## Naming conventions

| Element         | Convention                                                      |
| --------------- | --------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles` |
| Resource prefix | `{ctx.run_id}-<short>` (e.g. `{ctx.run_id}-s3-crud`)            |
| Context key     | snake_case string, e.g. `"s3_bucket"`, `"kms_key_id"`           |
| Setup function  | `setup_<group_name>` (underscores), e.g. `setup_s3_crud`        |
| Teardown fn     | `teardown_<group_name>`, e.g. `teardown_s3_crud`                |
| Service file    | Lowercase service name: `s3.py`, `cloudwatch_logs.py`           |

---

## Teardown rules (python-sdk-specific additions)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Python specifics:

- Wrap each individual delete call in `try: ... except Exception: pass` — never
  let one failure abort the rest of teardown.
- When deleting many objects from S3, use paginator `list_objects_v2` and batch
  them via `delete_objects`. For versioned buckets use `list_object_versions`
  and include both `Versions` and `DeleteMarkers`.
- Abort incomplete multipart uploads via `list_multipart_uploads` paginator
  before deleting a bucket that may have had in-progress uploads.
- Store resource IDs in `ctx["key"]` during setup so teardown can read them.
- Delete KMS aliases explicitly via `delete_alias` before scheduling key
  deletion — aliases are NOT removed automatically.
- Use `ctx.get("key")` (not `ctx["key"]`) in teardown to avoid `KeyError` when
  setup failed before storing the value.

---

## What agents must NOT do

- Never use `time.sleep` with a fixed duration — use a poll loop with a max
  retry count.
- Never hard-code the endpoint — always use `ctx.endpoint`.
- Never write to stdout inside a test function — the runner parses stdout as
  NDJSON.
- Never add a setup entry without a corresponding teardown entry in `TEARDOWN`.
- Never call `delete_bucket` without first emptying the bucket (objects,
  versions, delete markers, and incomplete multipart uploads).
- Never schedule KMS key deletion without first deleting any aliases pointing
  to that key.
