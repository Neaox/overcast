# Migrating from LocalStack

overcast is designed as a drop-in replacement for LocalStack Community Edition.
In most cases, changing `AWS_ENDPOINT_URL` is the only change needed.

This guide covers every known difference so you can migrate with confidence.

---

## Quick migration

```bash
# Before (LocalStack)
export AWS_ENDPOINT_URL=http://localhost:4566

# After (overcast) — same URL, different container
export AWS_ENDPOINT_URL=http://localhost:4566
```

Replace the container in your `docker-compose.yml`:

```yaml
# Before
services:
  localstack:
    image: localstack/localstack
    ports: ["4566:4566"]
    environment:
      SERVICES: s3,sqs,dynamodb
      DEBUG: 1

# After
services:
  overcast:
    image: ghcr.io/your-org/overcast:latest
    ports: ["4566:4566"]
    environment:
      OVERCAST_SERVICES: s3,sqs,dynamodb
      OVERCAST_LOG_LEVEL: debug
```

---

## Environment variable mapping

| LocalStack | overcast | Notes |
|------------|--------|-------|
| `LOCALSTACK_HOST` | `OVERCAST_HOST` | Hostname to bind. Default: `0.0.0.0` |
| `EDGE_PORT` | `OVERCAST_PORT` | Default: `4566` |
| `SERVICES` | `OVERCAST_SERVICES` | Comma-separated. Same service names. |
| `DATA_DIR` | `OVERCAST_DATA_DIR` | SQLite persistence directory |
| `DEBUG=1` | `OVERCAST_LOG_LEVEL=debug` | Verbose logging |
| `DEFAULT_REGION` | `OVERCAST_REGION` | Default: `us-east-1` |
| `GATEWAY_LISTEN` | `OVERCAST_HOST:OVERCAST_PORT` | Split into two variables |
| — | `OVERCAST_STATE=sqlite` | Explicit persistence opt-in (LocalStack uses `DATA_DIR` presence) |
| — | `OVERCAST_DEBUG=true` | Enable `/_debug/*` endpoints |
| — | `OVERCAST_TLS_CERT` / `OVERCAST_TLS_KEY` | HTTPS support |

---

## Endpoint mapping

| LocalStack | overcast | Notes |
|------------|--------|-------|
| `/_localstack/health` | `/_health` | Always enabled |
| `/_localstack/health` (detailed) | `/_debug/health` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/state/reset` | `/_debug/reset` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/info` | `/_debug/config` | Requires `OVERCAST_DEBUG=true` |
| `/_localstack/state` | `/_debug/state` | Requires `OVERCAST_DEBUG=true` |

---

## Behavioural differences

These are deliberate choices where overcast behaves differently from LocalStack.
Each is documented here so you know what to expect.

### S3: path-style addressing by default

overcast defaults to path-style S3 URLs (`http://localhost:4566/bucket/key`) rather
than virtual-hosted style (`http://bucket.localhost:4566/key`).

This matches what most local dev setups need. Virtual-hosted style requires DNS
resolution of `*.localhost` which doesn't work without extra configuration.

**Impact:** If your SDK is configured for virtual-hosted style, set:
```bash
# AWS CLI
aws configure set s3.addressing_style path

# Python boto3
s3 = boto3.client('s3', config=Config(s3={'addressing_style': 'path'}))
```

### Lambda: no real execution in v1

LocalStack Community executes Lambda functions (with limitations). overcast v1
returns a configurable stub response for Lambda invocations.

**Impact:** If your tests rely on real Lambda execution, they will need to be
updated once overcast Node.js execution is implemented (planned for v2).

To configure the stub response for a function:
```bash
curl -X PUT http://localhost:4566/_debug/lambda/my-function/stub \
  -H 'Content-Type: application/json' \
  -d '{"statusCode": 200, "body": "{\"result\": \"ok\"}"}'
```

### Persistence: explicit opt-in

LocalStack enables persistence when `DATA_DIR` is set. overcast requires an
explicit `OVERCAST_STATE=sqlite` in addition to `OVERCAST_DATA_DIR`.

This makes the intent unambiguous — you can set `OVERCAST_DATA_DIR` for other
purposes without accidentally enabling persistence.

### Request IDs: always present

overcast always includes `x-amz-request-id` (or `x-amzn-requestid`) on every
response including errors. Some LocalStack error responses omit this header.

---

## Known gaps (features LocalStack has that overcast doesn't yet)

| Feature | overcast status | Notes |
|---------|---------------|-------|
| Lambda execution | Stub only in v1 | Node.js execution planned |
| DynamoDB Streams | Planned | |
| SNS → SQS fan-out | In progress | |
| SQS → Lambda ESM | In progress | |
| CloudFormation | Out of scope | |
| IAM | Out of scope | All credentials accepted |
| SigV4 validation | TODO | Accepted but not validated |
| S3 multipart upload | Planned (P3) | |
| S3 versioning | Planned (P3) | |
| DynamoDB GSI | Planned (P3) | |
| DynamoDB transactions | Planned (P3) | |

If a feature you need is missing, check `docs/services/<service>.md` for the
detailed support matrix, then open an issue or PR.

---

## Troubleshooting

### "Connection refused" on port 4566

Confirm the container is running and healthy:
```bash
docker compose ps
curl http://localhost:4566/_health
```

### SDK returns "The specified bucket does not exist" for a bucket I just created

Check you're using path-style addressing (see above). Also confirm the bucket was
created against the same service instance — state is in-memory by default and
does not persist across container restarts.

### Tests pass with LocalStack but fail with overcast

1. Check `docs/services/<service>.md` — the operation may not yet be emulated.
2. Run with `OVERCAST_LOG_LEVEL=debug` to see exactly what request came in and
   what response went out.
3. Use `/_debug/state` to inspect the stored state.
4. If the operation is listed as ✅ Supported, open an issue with a minimal
   reproduction case.
