# AGENTS.md — java-sdk suite

> Conventions for AI agents and contributors planning or implementing
> `compat/suites/java-sdk/`.
>
> **Read [compat/AGENTS.md](../../AGENTS.md) first** — it contains the
> canonical teardown rules and separation boundary that apply to every suite.
> This file covers Java SDK-specific details for agents building this suite
> from scratch.
>
> For quick-start, prerequisites, and env vars see [README.md](README.md).

---

## What this suite tests

Every AWS service operation reachable via the **AWS SDK for Java v2**. It is
the Java column of the compatibility matrix. Failures on unimplemented services
are correct and expected — they are the coverage gap metric, not bugs to
silence.

The suite mirrors the `node-js-sdk` service and operation coverage but
validates Java-specific SDK behaviour (sync vs async clients, SDK retry
policies, `SdkClientException` wrapping, paginator API).

---

## Status

**Planned.** No implementation exists yet. Follow the implementation checklist
at the end of this file to build the suite from scratch.

---

## Runtime

| Item       | Value                                            |
| ---------- | ------------------------------------------------ |
| Language   | Java 17+                                         |
| Build tool | Maven 3.9+ (`pom.xml`)                           |
| AWS client | `software.amazon.awssdk:*` v2 (BOM-managed, pinned in `pom.xml`) |
| CI image   | `eclipse-temurin:17-alpine` + Maven                              |

> SDK upgrade policy: [compat/AGENTS.md § SDK version pinning](../../AGENTS.md#sdk-version-pinning--upgrade-strategy).

---

## File layout (planned)

```
compat/suites/java-sdk/
  AGENTS.md          ← you are here
  README.md          ← quick-start, prerequisites, env vars
  Dockerfile         ← eclipse-temurin:17-alpine + maven; builds and runs JAR
  pom.xml            ← AWS SDK BOM + all service dependencies

  src/
    main/
      java/
        io/overcast/compat/
          Main.java            ← entry point; runs suite; NDJSON to stdout
          harness/
            TestContext.java   ← TestContext class; inter-test state bag
            TestGroup.java     ← TestGroup, TestCase records (or classes)
            Runner.java        ← runSuite(), runGroup(), emitEvent()
          clients/
            AwsClients.java    ← lazy-init client factory
          groups/
            S3Group.java
            SqsGroup.java
            DynamoDbGroup.java
            SnsGroup.java
            LambdaGroup.java
            StsGroup.java
            KmsGroup.java
            SecretsManagerGroup.java
            SsmGroup.java
            IamGroup.java
            …
```

**One file per AWS service.** Never split a service across multiple group files
or merge two services into one file.

---

## Group anatomy

```java
// groups/S3Group.java
package io.overcast.compat.groups;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.harness.*;
import software.amazon.awssdk.services.s3.model.*;

import java.util.List;

public class S3Group {
    private final AwsClients clients;

    public S3Group(AwsClients clients) {
        this.clients = clients;
    }

    public List<TestGroup> getGroups(String suite) {
        return List.of(
            TestGroup.builder()
                .suite(suite)
                .service("s3")
                .name("s3-crud")
                .tests(List.of(
                    new TestCase("CreateBucket", this::createBucket),
                    new TestCase("ListBuckets",  this::listBuckets),
                    new TestCase("PutObject",    this::putObject),
                    new TestCase("GetObject",    this::getObject),
                    new TestCase("DeleteObject", this::deleteObject)
                ))
                .setup(this::setup)
                .teardown(this::teardown)
                .build()
        );
    }

    private void setup(TestContext ctx) {
        String bucket = ctx.runId() + "-s3-crud";
        clients.s3().createBucket(r -> r.bucket(bucket));
        ctx.set("bucket", bucket);
    }

    private void createBucket(TestContext ctx) {
        String bucket = ctx.get("bucket");
        boolean found = clients.s3().listBuckets().buckets().stream()
            .anyMatch(b -> b.name().equals(bucket));
        if (!found) throw new AssertionError(
            "bucket " + bucket + " not found after setup (runId=" + ctx.runId() + ")");
    }

    private void teardown(TestContext ctx) {
        String bucket = ctx.get("bucket");
        if (bucket == null) return;
        try { clients.s3().deleteBucket(r -> r.bucket(bucket)); }
        catch (Exception ignored) {}
    }
}
```

---

## Key types

```java
// harness/TestContext.java
public class TestContext {
    private final String endpoint;
    private final String region;
    private final String runId;
    private final Consumer<String> log;
    private final Map<String, Object> state = new HashMap<>();

    public String endpoint() { return endpoint; }
    public String region()   { return region; }
    public String runId()    { return runId; }
    public void   log(String msg) { log.accept(msg); }

    public void   set(String key, Object value) { state.put(key, value); }
    @SuppressWarnings("unchecked")
    public <T> T  get(String key) { return (T) state.get(key); }
}

// harness/TestCase.java
public record TestCase(String name, ThrowingConsumer<TestContext> fn) {}

// harness/TestGroup.java
public record TestGroup(
    String suite, String service, String name,
    List<TestCase> tests,
    ThrowingConsumer<TestContext> setup,    // nullable
    ThrowingConsumer<TestContext> teardown  // nullable
) {}
```

---

## Naming conventions

| Element         | Convention                                                      |
| --------------- | --------------------------------------------------------------- |
| Group name      | `<service>-<feature>` (kebab-case), e.g. `s3-crud`, `iam-roles` |
| Test name       | PascalCase AWS operation name, e.g. `CreateBucket`, `PutObject` |
| Resource prefix | `{runId}-<group-short>` e.g. `{runId}-s3-crud`                  |
| Group class     | `<Service>Group`, e.g. `S3Group`, `DynamoDbGroup`               |
| Group file      | `<Service>Group.java`                                           |
| Context key     | camelCase string, e.g. `"bucketName"`, `"queueUrl"`             |
| Package         | `io.overcast.compat.groups`                                     |

---

## Inter-test state

Use `ctx.set`/`ctx.get` to pass data between sequential tests within a group:

```java
// In setup:
ctx.set("bucketName", bucket);

// In a later test:
String bucket = ctx.get("bucketName");
if (bucket == null) throw new AssertionError("bucketName not set by setup");
```

Never rely on inter-group state. Never stash SDK client objects in the context.

---

## Teardown rules (Java-specific)

The canonical teardown rules are in [compat/AGENTS.md](../../AGENTS.md).
Additional Java specifics:

- Suppress teardown exceptions with `catch (Exception ignored) {}` — never let
  one cleanup failure abort subsequent deletes.
- Use `ctx.get("key")` in teardown — returns `null` (not an exception) when
  setup failed before setting the value.
- For S3, delete all objects (and versions for versioned buckets) before
  calling `deleteBucket`.
- `SdkServiceException` is the base for AWS errors; check
  `ex.statusCode() == 501` to detect unimplemented operations.
- Use SDK paginators (`s3.listObjectsV2Paginator`) for large result sets
  rather than manual loop + continuation tokens.

---

## Error messages

Throw `AssertionError` with a descriptive message:

```java
throw new AssertionError(
    "expected bucket " + bucket + " in listBuckets (runId=" + ctx.runId() + ")");
throw new AssertionError(
    "item not found after putItem: pk=" + pk);
```

---

## What agents must NOT do

- Never import from `internal/`, `router/`, or any Overcast server source tree.
- Never hard-code the endpoint — always configure clients from `ctx.endpoint()`.
- Never use `Thread.sleep` inside a test — use a poll loop with a max count.
- Never construct SDK clients inside test methods — inject via `AwsClients`.
- Never add a setup method without a corresponding teardown.
- Never call `deleteBucket` without first emptying the bucket.
- Never schedule KMS key deletion without first deleting aliases.
- Never write to `System.out` inside a test function — use `ctx.log()` for
  diagnostics; the runner parses stdout as NDJSON.

---

## Implementation checklist

When building this suite from scratch:

1. Create `pom.xml` using the AWS SDK for Java v2 BOM
   (`software.amazon.awssdk:bom`) and declare dependencies for each service
   module (`s3`, `sqs`, `dynamodb`, `sns`, `lambda`, `sts`, `kms`,
   `secretsmanager`, `ssm`, `iam`).
2. Implement `harness/TestContext.java`, `harness/TestGroup.java`,
   `harness/TestCase.java`, `harness/Runner.java`.
3. Implement `clients/AwsClients.java` — configure all clients with
   `endpointOverride(URI.create(ctx.endpoint()))`, fake region and
   credentials, and `forcePathStyle(true)` for S3.
4. Implement group classes starting with `S3Group.java`, mirroring the
   `node-js-sdk` group coverage.
5. Wire all groups in `Main.java`; run the suite and emit NDJSON to stdout.
6. Create `Dockerfile`: use `eclipse-temurin:17-alpine` + Maven; run
   `mvn package -q` and set `CMD` to `java -jar target/overcast-compat-*.jar`.
7. Register the suite in `compat/runner.go` and `compat/suites/registry.json`.
8. Run `mvn verify` to confirm no compilation or test errors.
9. Run the suite locally against a live Overcast instance and verify output.
