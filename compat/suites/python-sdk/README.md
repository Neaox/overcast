# python-sdk suite

Runs the full Overcast AWS compatibility matrix using **boto3** (AWS SDK for
Python v3, Python 3.11).

> **Status: implemented.** See [AGENTS.md](AGENTS.md) for code conventions.

Tests cover all services — including ones not yet implemented in Overcast.
Failures on unimplemented services are expected and are the coverage metric,
not a problem to fix.

---

## What it covers

All AWS services tested by the `node-js-sdk` suite, cross-validated with
boto3. Also tests Python-specific edge cases (e.g. `ResourceNotFoundException`
vs SDK error shapes, paginator patterns).

---

## Prerequisites

- Python 3.11+ (or any 3.9+ in a pinch)
- `pip install -r requirements.txt` (boto3, botocore)
- Overcast running on `http://localhost:4566`

---

## Running the suite

### Locally (Python 3.11+ required)

```bash
cd compat/suites/python-sdk
pip install -r requirements.txt

# Start Overcast first (separate terminal):
#   go run ./cmd/overcast -- serve
#   — or —
#   docker run -p 4566:4566 ghcr.io/your-org/overcast

python runner.py
```

### Via Docker (no local Python required)

```bash
# Build the suite image
docker build -t overcast-compat-python-sdk compat/suites/python-sdk

# Run against a local Overcast instance
docker run --rm --network host \
  -e OVERCAST_ENDPOINT=http://localhost:4566 \
  overcast-compat-python-sdk
```

### Via the Go CLI (recommended — runs all suites)

```bash
go run ./cmd/compat --endpoint http://localhost:4566
# or just this suite:
go run ./cmd/compat --endpoint http://localhost:4566 --suite python-sdk
```

---

## Environment variables

| Variable                  | Default                 | Description                        |
| ------------------------- | ----------------------- | ---------------------------------- |
| `OVERCAST_ENDPOINT`       | `http://localhost:4566` | Overcast base URL                  |
| `OVERCAST_DEFAULT_REGION` | `us-east-1`             | AWS region advertised to the SDK   |
| `OVERCAST_COMPAT_GROUPS`  | unset (all)             | Comma-separated group names to run |

---

## Architecture

```
python-sdk/
  Dockerfile          ← self-contained CI image (python:3.11-alpine)
  requirements.txt    ← boto3, botocore
  runner.py           ← entry point; imports all group modules; NDJSON output
  README.md           ← you are here

  lib/
    harness.py        ← TestContext, run_suite(), run_group(), is_unimplemented()
    clients.py        ← make_clients(endpoint, region) → named tuple of clients

  groups/             ← one file per AWS service
    s3.py
    sqs.py
    dynamodb.py
    sns.py
    …
```

### Key types (`lib/harness.py`)

| Type / function       | Purpose                                                                        |
| --------------------- | ------------------------------------------------------------------------------ |
| `TestContext`         | Dict-like; has `endpoint`, `region`, `run_id`, `log`; plus a `[str]` state bag |
| `run_suite(groups)`   | Runs all groups; emits NDJSON to stdout                                        |
| `is_unimplemented(e)` | Returns `True` if the exception wraps an HTTP 501 response                     |

### Group modules (`groups/`)

Each service file exports three module-level dicts:

```python
IMPLS: dict[str, dict[str, Callable[[TestContext], None]]]
SETUP: dict[str, Callable[[TestContext], None]]   # keyed by group name
TEARDOWN: dict[str, Callable[[TestContext], None]] # keyed by group name
```

Individual test functions raise `AssertionError` (or any exception) to fail;
return normally to pass. Teardown functions must never raise.

---

## Adding a new group

1. Open (or create) `groups/<service>.py` — one file per AWS service.
2. Add entries to `IMPLS`, `SETUP`, and `TEARDOWN` maps.
3. Register the module in `runner.py`.
4. Run `python runner.py` locally to verify NDJSON output is well-formed.

See [AGENTS.md](AGENTS.md) for detailed code conventions and teardown rules.
