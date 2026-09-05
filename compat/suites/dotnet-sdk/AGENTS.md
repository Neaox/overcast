# AGENTS.md — dotnet-sdk suite

> Conventions for AI agents and contributors working in
> `compat/suites/dotnet-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules, separation boundary, and the `group:test`
> implementation-key rules that apply to every suite.
> This file covers only dotnet-sdk-specific details.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

Every AWS service operation reachable via the **AWS SDK for .NET v4**'s async
clients. It is the .NET column of the compatibility matrix. Failures on
unimplemented services are correct and expected — they are the coverage gap
metric, not bugs to silence.

The suite follows the shared registry contract
(`registry.json` — see
[compat/AGENTS.md § Uniformity](../../AGENTS.md#2-uniformity--the-registry-is-the-contract))
while validating .NET-specific SDK behaviour: `async`/`await` throughout,
`AmazonServiceException` shapes, and the SDK's paginator API.

---

## Status

**Implemented**, covering fewer services than `java-sdk` or `go-sdk` today.
`ServiceGroups.All()` wires 11 service group classes: S3, SQS, DynamoDB, SNS,
Lambda, STS, KMS, Secrets Manager, SSM, IAM and EventBridge. It runs in the
compat CI matrix (`.github/workflows/compat.yml`) alongside every other
suite. A `Tests/` xunit project (registration tests only — see below) was
added in [#1697](https://github.com/overcast-sh/overcast/pull/1697) and
extended in [#1725](https://github.com/overcast-sh/overcast/pull/1725).

---

## Runtime

| Item        | Value                                                        |
| ----------- | -------------------------------------------------------------- |
| Language    | C# 12 / .NET 8                                                 |
| AWS client  | `AWSSDK.*` NuGet packages, pinned to `4.0.0` in `OvercastCompat.csproj` |
| Test client | xunit `2.9.2` (`Tests/OvercastCompat.Tests.csproj`)             |
| CI image    | `mcr.microsoft.com/dotnet/sdk:8.0-alpine` (build stage) → `mcr.microsoft.com/dotnet/runtime:8.0-alpine` (runtime) |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout

```
compat/suites/dotnet-sdk/
  AGENTS.md                  ← you are here
  README.md                  ← quick-start, prerequisites, env vars, architecture
  Dockerfile                 ← .NET SDK build stage (runs Tests/ too) + runtime stage
  run.sh                     ← builds/runs the Dockerfile; invoked by cmd/compat as `sh run.sh`
  OvercastCompat.csproj      ← AWSSDK.* NuGet references; OutputType=Exe
  Program.cs                 ← top-level statement entry point

  Harness/
    TestContext.cs           ← per-group state bag
    TestTypes.cs              ← TestFn/SetupFn delegates; TestCase/TestGroup records
    Runner.cs                 ← runs groups, emits NDJSON to stdout
    InteractiveRunner.cs      ← OVERCAST_COMPAT_INTERACTIVE command protocol
    Assertions.cs             ← assertion helpers

  Clients/
    AwsClients.cs             ← lazily-initialised per-service client factory

  Registry/
    RegistryLoader.cs         ← loads registry.json + registry.generated.json, builds groups;
                                 declares the internal ScenarioBackend delegate

  Groups/
    IServiceGroup.cs          ← interface every group class implements
    ServiceGroups.cs          ← ServiceGroups.All(clients) — every group, in registration order
    S3Group.cs
    SqsGroup.cs
    DynamoDbGroup.cs
    …                         ← one file per AWS service, 11 in total

  Tests/
    OvercastCompat.Tests.csproj ← separate xunit project; references OvercastCompat.csproj
    RegistrationTests.cs        ← this suite's own registrations vs. real registry.json
    GeneratedRegistryTests.cs   ← registry.generated.json loading + ScenarioBackend resolution
```

**One file per AWS service.** Never split a service across multiple group
files or merge two services into one file.

`Tests/` is its own project deliberately: `OvercastCompat.csproj` excludes
`Tests/**` from its own compile glob (`<Compile Remove="Tests/**" />`), and
grants it `InternalsVisibleTo`, so the test project can call the suite's
`internal` registry types without them being public API.

---

## Group anatomy

A group class implements `IServiceGroup`: three read-only dictionaries
(`Impls`, `Setups`, `Teardowns`) merged into the suite's global registry at
startup. This is a trimmed excerpt of the real `S3Group`
(`Groups/S3Group.cs`):

```csharp
using Amazon.S3;
using Amazon.S3.Model;
using OvercastCompat.Clients;
using OvercastCompat.Harness;

namespace OvercastCompat.Groups;

public sealed class S3Group(AwsClients clients) : IServiceGroup
{
    public IReadOnlyDictionary<string, TestFn> Impls() => new Dictionary<string, TestFn>(StringComparer.Ordinal)
    {
        ["s3-crud:CreateBucket"] = CreateBucketAsync,
        ["s3-crud:PutObject"] = PutObjectAsync,
        // … one entry per test, keyed "group:test"
    };

    public IReadOnlyDictionary<string, SetupFn> Setups() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["s3-crud"] = SetupCrudAsync,
    };

    public IReadOnlyDictionary<string, SetupFn> Teardowns() => new Dictionary<string, SetupFn>(StringComparer.Ordinal)
    {
        ["s3-crud"] = context => EmptyAndDeleteBucketAsync(context.GetString("s3Bucket")),
    };

    private async Task SetupCrudAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3crud";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        context.Set("s3Bucket", bucket);
    }

    // Creates and tears down its own bucket, rather than the shared one from
    // setup, so it can assert CreateBucket's effect in isolation.
    private async Task CreateBucketAsync(TestContext context)
    {
        var bucket = $"{context.RunId}-s3create";
        await clients.S3().PutBucketAsync(new PutBucketRequest { BucketName = bucket });
        try
        {
            var response = await clients.S3().ListBucketsAsync();
            Assertions.True(response.Buckets.Any(item => item.BucketName == bucket),
                $"CreateBucket: bucket {bucket} not found in ListBuckets (runId={context.RunId})");
        }
        finally
        {
            await EmptyAndDeleteBucketAsync(bucket);
        }
    }

    private async Task PutObjectAsync(TestContext context)
    {
        var bucket = RequireBucket(context);
        await clients.S3().PutObjectAsync(new PutObjectRequest
        {
            BucketName = bucket, Key = "test-key", ContentBody = "hello world",
        });
        var head = await clients.S3().GetObjectMetadataAsync(new GetObjectMetadataRequest
        {
            BucketName = bucket, Key = "test-key",
        });
        Assertions.GreaterThan(0, head.Headers.ContentLength, "PutObject: ContentLength should be > 0");
    }

    // Reads the group's shared bucket, set by SetupCrudAsync; throws if a test
    // runs before setup did, rather than letting a null bucket name reach the SDK.
    private static string RequireBucket(TestContext context) =>
        context.GetString("s3Bucket") ?? throw new InvalidOperationException("s3Bucket not set");

    private async Task EmptyAndDeleteBucketAsync(string? bucket)
    {
        if (bucket is null) return;
        try { /* delete objects/versions/multipart uploads, then */ await clients.S3().DeleteBucketAsync(new DeleteBucketRequest { BucketName = bucket }); }
        catch { /* ignore */ }
    }
}
```

Clients come from `clients.S3()`, `clients.SQS()`, etc. — never construct an
AWS SDK client inside a test method.

---

## Key types

```csharp
// Harness/TestTypes.cs
public delegate Task TestFn(TestContext context);
public delegate Task SetupFn(TestContext context);

public sealed record TestCase(
    string Name, TestFn Fn,
    string? Op = null, string? Skip = null,
    IReadOnlyList<string>? Depends = null);
    // Op: AWS operation name for doc links (null = use Name)
    // Skip: non-empty string emits this test as "skip" instead of running it
    // Depends: names of other tests in the same group that must pass first

public sealed record TestGroup(
    string Suite, string Service, string Name,
    IReadOnlyList<TestCase> Tests,
    SetupFn? Setup = null, SetupFn? Teardown = null);

// Harness/TestContext.cs
public sealed class TestContext
{
    public string Endpoint { get; }
    public string Region   { get; }
    public string RunId    { get; }
    public void   Set(string key, object? value) { … }
    public T?     Get<T>(string key) { … }
    public string? GetString(string key) { … } // Get<string>(key)
    public void   Log(string message) { … }     // stderr only — stdout is NDJSON
}
```

`IServiceGroup` (`Groups/IServiceGroup.cs`) is the contract every group class
implements: `Impls()`, `Setups()`, `Teardowns()`, plus a default `SourceName`
(the type's name) used to label a duplicate-key error.

---

## Naming conventions

| Element         | Convention                                                        |
| --------------- | -------------------------------------------------------------------- |
| Impl map key    | `<group>:<test>` — **always group-qualified**, never bare (see [compat/AGENTS.md § Implementation keys](../../AGENTS.md#implementation-keys--grouptest-and-a-bad-key-aborts-the-run)) |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles`     |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `PutObject`     |
| Resource prefix | `{context.RunId}-<short>`, e.g. `{RunId}-s3crud`                    |
| Group class     | `<Service>Group`, e.g. `S3Group`, `DynamoDbGroup`                   |
| Group file      | `<Service>Group.cs`, matching the class name                       |
| Context key     | camelCase string, e.g. `"s3Bucket"`, `"kmsKeyId"`                   |
| Namespace       | `OvercastCompat.Groups`                                             |

---

## Inter-test state

Use `context.Set`/`context.Get<T>` (or `context.GetString` for the common
string case) to pass data between sequential tests within a group:

```csharp
// In setup:
context.Set("s3Bucket", bucket);

// In a later test:
var bucket = context.GetString("s3Bucket")
    ?? throw new InvalidOperationException("s3Bucket not set");
```

Never rely on inter-group state — a fresh `TestContext` is created per group.
Never stash an AWS SDK client object in the context; it already lives in
`AwsClients`.

---

## Teardown rules (.NET-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional .NET specifics:

- Suppress teardown exceptions with an empty `catch { }` block — never let one
  cleanup failure abort subsequent deletes.
- Use `context.GetString("key")` in teardown — it returns `null` (not an
  exception) when setup failed before setting the value.
- For S3, abort incomplete multipart uploads and delete all object versions
  and delete markers before calling `DeleteBucketAsync`.
- `AmazonServiceException` is the base exception for AWS error responses;
  check `ex.StatusCode == HttpStatusCode.NotImplemented` (501) to detect an
  unimplemented operation.
- Use the SDK's paginator API (e.g. `AmazonS3Client.Paginators.ListObjectsV2`)
  for large result sets rather than manually looping `ListObjectsV2Async`.

---

## Error messages

Throw via the `Assertions` static class (`Harness/Assertions.cs`) rather than
a raw `throw new InvalidOperationException(...)` where one of its methods
fits — it keeps the message shape consistent across groups. It covers:
`True`, `False`, `NotNull`, `NotBlank`, `Equal<T>`, `GreaterThan`,
`GreaterThanOrEqual`. Every method throws `InvalidOperationException` built
from the message you pass it; `Equal`, `GreaterThan` and
`GreaterThanOrEqual` append the expected-vs-actual values themselves:

```csharp
Assertions.True(found,
    $"ListObjectsV2: test-key not found (runId={context.RunId})");
Assertions.Equal("hello world", body, "GetObject: body mismatch");
```

---

## Generated groups and the `ScenarioBackend` hook

`RegistryLoader.cs` loads `registry.json` and its generated sibling
`registry.generated.json` (concatenated, hand-written groups first — see
[compat/AGENTS.md § registry.json](../../AGENTS.md#registryjson--canonical-test-matrix)),
validates this suite's impl keys against them, and builds the `TestGroup`
list `Runner.RunSuiteAsync` executes.

A **generated** group (one with `"generated": true` in
`registry.generated.json`) is not implemented by a registered impl the way a
hand-written group is — it is meant to be executed by an interpreter reading
the group's scenario IR. The internal `ScenarioBackend` delegate
(declared at the top of `Registry/RegistryLoader.cs`) is the extension point
that interpreter plugs into: it is the last resolution step, after the
group-qualified and bare impl-key lookups and before the not-implemented
sentinel.

**Nothing implements `ScenarioBackend` in this suite yet.**
`registry.generated.json` is currently empty (`"groups": []`) — see its own
`comment` field — so this has no observable effect today. Once the generator
starts emitting groups scoped to `dotnet-sdk`, a generated group this suite
has no backend for reports as a hard **failure** naming the group
(`generated group "<group>" is scoped to dotnet-sdk but dotnet-sdk has no
scenario backend`), not a skip. Do not add a `ScenarioBackend` implementation
speculatively; wait until there is a real interpreter to wire in.

---

## Registration tests

`Tests/RegistrationTests.cs` runs on `dotnet test` (also inside the Docker
build — see the Dockerfile) and resolves this suite's real registrations
against the real `registry.json`, without starting a run:

- `RegisteredImplsResolveAgainstRegistry` — every impl key must resolve to a
  real `group:test` pair.
- `RegisteredImplsHaveNoDuplicateKeys` — no two `IServiceGroup` classes may
  register the same key (last-writer-wins would otherwise silently drop one
  implementation).
- `RegisteredImplKeysAreAllQualified` — every impl-map key must be
  group-qualified (`group:test`); a bare key is refused, because it silently
  becomes ambiguous the moment a second group declares the same test name
  (see [compat/AGENTS.md § Implementation keys](../../AGENTS.md#implementation-keys--grouptest-and-a-bad-key-aborts-the-run)).

`Tests/GeneratedRegistryTests.cs` covers the loader itself (build/validate
behaviour and `registry.generated.json`/`ScenarioBackend` handling)
independently of this suite's own registrations.

This project did not exist before
[#1697](https://github.com/overcast-sh/overcast/pull/1697) — until then this
suite's only guard against a mis-binding was the abort-at-startup behaviour
every loader has (see
[compat/AGENTS.md § Implementation keys](../../AGENTS.md#implementation-keys--grouptest-and-a-bad-key-aborts-the-run)),
not a check that runs ahead of a real suite run.

Run just these with `dotnet test Tests/OvercastCompat.Tests.csproj -c Release`
from this directory — no live Overcast instance required.

---

## Adding a new group

1. Add the group and its tests to
   [compat/suites/registry.json](../registry.json) first (see
   [compat/AGENTS.md § Uniformity](../../AGENTS.md#2-uniformity--the-registry-is-the-contract)) —
   or confirm they are already there if another suite added them first.
2. Create or open `Groups/<Service>Group.cs`, implementing `IServiceGroup`.
3. Register every test under a **group-qualified** key (`"<group>:<test>"`)
   in `Impls()`, and its setup/teardown (if any) in
   `Setups()`/`Teardowns()`.
4. Add the new group instance to `ServiceGroups.All()` if the service is new.
5. Add any new AWS SDK service package to `OvercastCompat.csproj` and to
   `Clients/AwsClients.cs` as a lazily-initialised property.
6. Run `dotnet test Tests/OvercastCompat.Tests.csproj -c Release` to confirm
   the registration tests pass.
7. Run `dotnet build OvercastCompat.csproj -c Release` and run the published
   app against a live Overcast instance (see
   [README.md](README.md#running-the-suite)) to verify the NDJSON output.

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source
  tree — see [compat/AGENTS.md § Separation boundary](../../AGENTS.md#separation-boundary--non-negotiable).
- Never hard-code the endpoint — always go through `AwsClients`, which is
  configured from `context.Endpoint`/the `OVERCAST_ENDPOINT` environment
  variable at startup.
- Never use `Thread.Sleep` — use `await Task.Delay` inside a poll loop with a
  maximum retry count if async waiting is genuinely necessary.
- Never construct an AWS SDK client inside a test method — obtain it from the
  injected `AwsClients`.
- Never register an impl key without the `group:` qualifier — the
  registration tests reject it, and CI's `dotnet test` step fails the image
  build.
- Never add a setup delegate without a corresponding teardown delegate.
- Never call `DeleteBucketAsync` without first emptying the bucket (objects,
  versions, delete markers, incomplete multipart uploads).
- Never schedule KMS key deletion without first deleting any aliases pointing
  to that key.
- Never write to `Console.Out` inside a test — the runner parses stdout as
  NDJSON; use `context.Log(...)` (stderr) for diagnostics.
- Never add code to `Tests/` that talks to a live Overcast instance — it runs
  as part of `dotnet test`/the Docker image build and must stay a pure
  registry-loader check.
