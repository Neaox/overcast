# Lambda

> AWS docs: https://docs.aws.amazon.com/lambda/latest/api/welcome.html

Lambda emulation has two distinct concerns:

1. **Control plane** — the management API (create/update/invoke functions, manage
   event source mappings). This is straightforward HTTP.
2. **Data plane** — actually executing function code. This requires running
   arbitrary user code, which has significant security and complexity implications.

For v1, the data plane is **stub-mode only**: `Invoke` calls return a configurable
canned response rather than executing real code. True execution is planned for
a later milestone using container-in-container (DinD) or a lightweight
`runc`-based sandbox.

---

## Summary

| Category | ✅ Supported | ⚠️ Partial | 🚧 WIP | ❌ Unsupported |
|----------|------------|-----------|--------|--------------|
| Function management | 0 | 0 | 0 | 8 |
| Invocation | 0 | 0 | 0 | 3 |
| Aliases & versions | 0 | 0 | 0 | 6 |
| Event source mappings | 0 | 0 | 0 | 4 |
| Layers | 0 | 0 | 0 | 4 |
| Concurrency & configuration | 0 | 0 | 0 | 5 |

---

## Endpoints

### Function management

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateFunction` | ❌ Unsupported | Stores metadata; code is not executed | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_CreateFunction.html) |
| `DeleteFunction` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_DeleteFunction.html) |
| `GetFunction` | ❌ Unsupported | Returns stored metadata | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetFunction.html) |
| `GetFunctionConfiguration` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetFunctionConfiguration.html) |
| `UpdateFunctionCode` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_UpdateFunctionCode.html) |
| `UpdateFunctionConfiguration` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_UpdateFunctionConfiguration.html) |
| `ListFunctions` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_ListFunctions.html) |
| `GetFunctionCodeSigningConfig` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetFunctionCodeSigningConfig.html) |

### Invocation

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `Invoke` (synchronous) | ❌ Unsupported | Stub mode: returns configurable canned response | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html) |
| `InvokeAsync` (deprecated) | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_InvokeAsync.html) |
| `InvokeWithResponseStream` | ❌ Unsupported | Streaming response support | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_InvokeWithResponseStream.html) |

### Aliases & versions

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `PublishVersion` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_PublishVersion.html) |
| `ListVersionsByFunction` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_ListVersionsByFunction.html) |
| `CreateAlias` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_CreateAlias.html) |
| `DeleteAlias` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_DeleteAlias.html) |
| `GetAlias` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetAlias.html) |
| `ListAliases` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_ListAliases.html) |

### Event source mappings

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `CreateEventSourceMapping` | ❌ Unsupported | SQS→Lambda, SNS→Lambda, DynamoDB Streams→Lambda | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_CreateEventSourceMapping.html) |
| `DeleteEventSourceMapping` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_DeleteEventSourceMapping.html) |
| `GetEventSourceMapping` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetEventSourceMapping.html) |
| `ListEventSourceMappings` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_ListEventSourceMappings.html) |

### Layers

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `PublishLayerVersion` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_PublishLayerVersion.html) |
| `GetLayerVersion` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetLayerVersion.html) |
| `ListLayers` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_ListLayers.html) |
| `DeleteLayerVersion` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_DeleteLayerVersion.html) |

### Concurrency & configuration

| Operation | Status | Notes | AWS Docs |
|-----------|--------|-------|----------|
| `PutFunctionConcurrency` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_PutFunctionConcurrency.html) |
| `GetFunctionConcurrency` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetFunctionConcurrency.html) |
| `DeleteFunctionConcurrency` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_DeleteFunctionConcurrency.html) |
| `PutProvisionedConcurrencyConfig` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_PutProvisionedConcurrencyConfig.html) |
| `GetProvisionedConcurrencyConfig` | ❌ Unsupported | | [docs](https://docs.aws.amazon.com/lambda/latest/api/API_GetProvisionedConcurrencyConfig.html) |

---

## Known limitations

- **No real code execution in v1.** `Invoke` returns a canned response. To
  configure the response for a function, `PUT` to the emulator-specific endpoint
  `/_emulator/lambda/<function-name>/stub` with a JSON body `{ "statusCode": 200, "body": "..." }`.
- Cold-start simulation is not implemented. All invocations return immediately.
- Container images (`PackageType: Image`) are accepted in `CreateFunction` but
  the image is not pulled or executed.
- Runtime-specific environment (Node, Python, Go runtimes) is not validated.
