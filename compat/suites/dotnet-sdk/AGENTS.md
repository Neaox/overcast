# AGENTS.md — dotnet-sdk suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/dotnet-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers .NET SDK-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

Every AWS service operation reachable via the **AWS SDK for .NET v3**. It is
the .NET column of the compatibility matrix. Failures on unimplemented services
are correct and expected — they are the coverage gap metric, not bugs to
silence.

The suite mirrors the `node-js-sdk` service and operation coverage but
validates .NET-specific SDK behaviour (async/await patterns, paginator API,
exception shapes, retry handling).

---

## Status

**Implemented.** Covers 10 AWS services: S3 (7 groups), SQS (4 groups), DynamoDB (6 groups), SNS (3 groups), Lambda (5 groups), STS (2 groups), KMS (3 groups), Secrets Manager (2 groups), SSM (3 groups), IAM (4 groups).

---

## Runtime

| Item       | Value                                           |
| ---------- | ----------------------------------------------- |
| Language   | C# 12 / .NET 8+                                 |
| AWS client | `AWSSDK.*` NuGet packages v4.0.0 (pinned in `.csproj`)   |
| CI image   | `mcr.microsoft.com/dotnet/sdk:8.0-alpine`                 |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout (planned)

```
compat/suites/dotnet-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← mcr.microsoft.com/dotnet/sdk:8.0-alpine; publishes self-contained binary
  OvercastCompat.sln
  src/
    OvercastCompat/
      OvercastCompat.csproj  ← AWSSDK.* NuGet references; OutputType=Exe
      Program.cs             ← entry point; runs all groups; NDJSON to stdout
      Harness/
        TestContext.cs       ← TestContext record; inter-test state bag
        TestGroup.cs         ← TestGroup, TestCase records
        Runner.cs            ← RunSuiteAsync(), RunGroupAsync(), EmitEvent()
      Clients/
        AwsClients.cs        ← AwsClients record; lazy-init per-service clients
      Groups/
        S3Group.cs
        SqsGroup.cs
        DynamoDbGroup.cs
        SnsGroup.cs
        LambdaGroup.cs
        StsGroup.cs
        KmsGroup.cs
        SecretsManagerGroup.cs
        SsmGroup.cs
        IamGroup.cs
        …
```

**One file per AWS service.** Never split a service across files.

---

## Group anatomy

```csharp
// Groups/S3Group.cs
using Amazon.S3;
using Amazon.S3.Model;

namespace OvercastCompat.Groups;

public sealed class S3Group
{
    private readonly AwsClients _clients;
    private string? _bucket;

    public S3Group(AwsClients clients) => _clients = clients;

    public IEnumerable<TestGroup> GetGroups(string suite) =>
    [
        new TestGroup
        {
            Suite   = suite,
            Service = "s3",
            Name    = "s3-crud",
            Tests   =
            [
                new TestCase("CreateBucket",  CreateBucketAsync),
                new TestCase("ListBuckets",   ListBucketsAsync),
                new TestCase("PutObject",     PutObjectAsync),
                new TestCase("GetObject",     GetObjectAsync),
                new TestCase("DeleteObject",  DeleteObjectAsync),
            ],
            Setup    = SetupAsync,
            Teardown = TeardownAsync,
        },
    ];

    private async Task SetupAsync(TestContext ctx)
    {
        _bucket = $"{ctx.RunId}-s3-crud";
        await _clients.S3.PutBucketAsync(new PutBucketRequest { BucketName = _bucket });
    }

    private async Task CreateBucketAsync(TestContext ctx)
    {
        var resp = await _clients.S3.ListBucketsAsync();
        if (!resp.Buckets.Any(b => b.BucketName == _bucket))
            throw new InvalidOperationException(
                $"bucket {_bucket} not found after setup (runId={ctx.RunId})");
    }

    private async Task TeardownAsync(TestContext ctx)
    {
        if (_bucket is null) return;
        try { await _clients.S3.DeleteBucketAsync(new DeleteBucketRequest { BucketName = _bucket }); }
        catch { /* ignore */ }
    }
}
```

---

## Key types

```csharp
// Harness/TestContext.cs
public record TestContext
{
    public required string Endpoint { get; init; }
    public required string Region   { get; init; }
    public required string RunId    { get; init; }
    public required Action<string> Log { get; init; }

    // Inter-test state bag
    private readonly Dictionary<string, object?> _state = new();
    public void   Set(string key, object? value) => _state[key] = value;
    public T?     Get<T>(string key) => _state.TryGetValue(key, out var v) ? (T?)v : default;
}

// Harness/TestGroup.cs
public record TestCase(string Name, Func<TestContext, Task> Fn);

public record TestGroup
{
    public required string  Suite   { get; init; }
    public required string  Service { get; init; }
    public required string  Name    { get; init; }
    public required IEnumerable<TestCase> Tests { get; init; }
    public Func<TestContext, Task>? Setup    { get; init; }
    public Func<TestContext, Task>? Teardown { get; init; }
}
```

---

## Naming conventions

| Element         | Convention                                                       |
| --------------- | ---------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles`  |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `PutObject`  |
| Resource prefix | `{ctx.RunId}-<group-short>` e.g. `{ctx.RunId}-s3-crud`           |
| Group class     | `<Service>Group`, e.g. `S3Group`, `DynamoDbGroup`                |
| Group file      | `<Service>Group.cs`, e.g. `S3Group.cs`, `SecretsManagerGroup.cs` |
| Context key     | PascalCase string, e.g. `"BucketName"`, `"QueueUrl"`             |

---

## Inter-test state

Use `ctx.Set`/`ctx.Get<T>` to pass data between sequential tests within a
group. Example:

```csharp
// In setup or an early test:
ctx.Set("BucketName", _bucket);

// In a later test:
var bucket = ctx.Get<string>("BucketName")
    ?? throw new InvalidOperationException("BucketName not set");
```

Never rely on inter-group state — `ctx` is fresh for every group run. Never
stash SDK client objects in the context bag.

---

## Teardown rules (.NET-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional .NET specifics:

- Suppress teardown exceptions with an empty `catch { }` block — never let
  one cleanup failure abort subsequent deletes.
- Use `ctx.Get<T>` (not a direct field) in teardown — if setup failed before
  setting data, `Get<T>` returns `default` rather than throwing.
- For S3, delete all objects (and versions for versioned buckets) before
  calling `DeleteBucketAsync`.
- `AmazonServiceException` is the base exception for AWS errors; check
  `ex.StatusCode == HttpStatusCode.NotImplemented` (501) to detect
  unimplemented operations.
- Use `AmazonS3Client.Paginators.ListObjectsV2` for paginated object listing
  rather than manually calling `ListObjectsV2Async` in a loop.

---

## Error messages

Format assertion exceptions to identify what failed:

```csharp
throw new InvalidOperationException(
    $"expected bucket {bucket} in ListBuckets (runId={ctx.RunId})");
throw new InvalidOperationException(
    $"item not found after PutItem: pk={pk} (runId={ctx.RunId})");
```

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — always use `ctx.Endpoint`.
- Never use `Thread.Sleep` — use `await Task.Delay` inside a poll loop with a
  max count if async waiting is unavoidable.
- Never construct SDK clients inside test methods — inject them via
  `AwsClients` in the constructor.
- Never add a setup delegate without a corresponding teardown delegate.
- Never call `DeleteBucketAsync` without first emptying the bucket.
- Never schedule KMS key deletion without first deleting any aliases.
- Never write to `Console.Out` inside a test function — the runner parses
  stdout as NDJSON; write diagnostics to `ctx.Log` (stderr).

---

## Implementation checklist

When building this suite from scratch:

1. Create `OvercastCompat.sln` and `src/OvercastCompat/OvercastCompat.csproj`
   with `OutputType=Exe` and NuGet references to `AWSSDK.S3`,
   `AWSSDK.SQS`, `AWSSDK.DynamoDBv2`, `AWSSDK.SimpleNotificationService`,
   `AWSSDK.Lambda`, `AWSSDK.SecurityToken`, `AWSSDK.KeyManagementService`,
   `AWSSDK.SecretsManager`, `AWSSDK.SimpleSystemsManagement`,
   `AWSSDK.IdentityManagement`.
2. Create `Harness/TestContext.cs`, `Harness/TestGroup.cs`,
   `Harness/Runner.cs` with the types above.
3. Create `Clients/AwsClients.cs` — all clients configured with
   `ServiceURL = ctx.Endpoint`, `AuthenticationRegion = ctx.Region`,
   fake credentials, and `ForcePathStyle = true` for S3.
4. Implement group classes starting with `S3Group.cs`, mirroring the
   `node-js-sdk` group coverage.
5. Wire all groups in `Program.cs`; run `RunSuiteAsync` and emit NDJSON.
6. Create `Dockerfile` based on `mcr.microsoft.com/dotnet/sdk:8.0-alpine`;
   publish a self-contained binary.
7. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
8. Run `dotnet build` to confirm no compilation errors.
9. Run the suite locally against a live Overcast instance and verify output.
