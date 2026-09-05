# java-sdk suite

Runs the Overcast AWS compatibility matrix using the **AWS SDK for Java v2**
(Java 17+, built with Maven, packaged as a self-contained JAR).

> **Status: implemented.** See [AGENTS.md](AGENTS.md) for code conventions.

Tests cover every AWS service the registry declares — including ones not yet
implemented in Overcast. Failures on unimplemented services are expected and
are the coverage metric, not a problem to fix.

---

## What it covers

Every operation the suite's service groups register, cross-validated with the
AWS SDK for Java v2's sync clients: S3, SQS, DynamoDB, SNS, Lambda, STS, KMS,
Secrets Manager, SSM, IAM, Kinesis, CloudWatch Logs, SES, EventBridge,
CloudFormation, EC2, ECS, Cognito, AppSync, API Gateway (v1 and v2),
CloudFront, RDS, Step Functions, EventBridge Pipes, WAFv2, Shield,
ElastiCache and EFS — see `Main.serviceGroups()` for the authoritative,
registration-order list. Every registry test the suite has not implemented is
still emitted as a `skip`, so the coverage gap is visible rather than silent.

The suite also loads [compat/suites/registry.generated.json](../registry.generated.json):
a group marked there for `java-sdk` is executed through a `ScenarioBackend`
rather than a hand-written method. No backend is wired in yet, so a generated
group scoped to this suite currently fails loudly (naming the group) instead
of silently skipping — see [AGENTS.md § Generated groups](AGENTS.md#generated-groups-and-the-scenariobackend-hook).

## Prerequisites

- Java 17 or newer (the suite compiles with `--release 17`; verified here
  against a Java 25 JDK)
- Maven 3.9+ — no wrapper is checked into this suite, so a local run needs
  `mvn` on `PATH`, or the Docker path below
- Docker, only for the "via Docker" and "via the Go CLI" paths
- Overcast running somewhere reachable — see [compat/AGENTS.md § Running a session](../../AGENTS.md#running-a-session--ports-are-chosen-never-assumed)
  for why `4566`/`4567` are off-limits for a test instance you start yourself

---

## Running the suite

### Locally (Maven required)

```bash
cd compat/suites/java-sdk
mvn -B package        # also runs the registry-loader unit tests; produces target/java-sdk-compat-*.jar

# Start Overcast first (separate terminal), e.g.:
#   go run ./cmd/overcast -- serve

OVERCAST_ENDPOINT=http://localhost:4566 java -jar target/java-sdk-compat-*.jar
```

PowerShell:

```powershell
cd compat/suites/java-sdk
mvn -B package

$env:OVERCAST_ENDPOINT = "http://localhost:4566"
java -jar (Get-Item target/java-sdk-compat-*.jar)
```

`mvn -B test` runs just the registry-loader unit tests
([AGENTS.md § Registration tests](AGENTS.md#registration-tests)) without
building the JAR or touching a live Overcast instance — useful for checking a
new group's impl keys resolve before running the full suite.

### Via Docker (no local Maven required)

This suite ships its own image. The **build context is `compat/suites/`**, not
this directory, because the image also copies in the shared
`compat/suites/registry.json` (see [compat/AGENTS.md § Running suites](../../AGENTS.md#running-suites-docker--ci)):

```bash
docker build -f compat/suites/java-sdk/Dockerfile -t oc-java-sdk-compat compat/suites
docker run --rm --network host \
  -e OVERCAST_ENDPOINT=http://localhost:4566 \
  oc-java-sdk-compat
```

On Docker Desktop (verified here on Windows; macOS runs the same
Linux-VM architecture and is expected to behave the same way),
`--network host` does not share the host's network stack the way it does on
native Linux, so a container cannot reach an Overcast process listening on
the host's `localhost` this way — point it at `host.docker.internal` instead:

```bash
docker run --rm \
  -e OVERCAST_ENDPOINT=http://host.docker.internal:4566 \
  oc-java-sdk-compat
```

`run.sh` (used by the paths below) already picks the right network mode when
it runs *inside* a container itself; this distinction only matters when you
invoke Docker by hand from a Windows or macOS shell.

### Via the Go CLI (recommended — runs all suites, or just this one)

```bash
# Starts its own Overcast instance on a free port and stops it afterwards:
go run ./cmd/compat
# or just this suite:
go run ./cmd/compat --suite java-sdk
# or against an instance you are already running:
go run ./cmd/compat --endpoint http://localhost:4566 --suite java-sdk
```

This is what CI runs. `cmd/compat` invokes `sh run.sh` for this suite (see
[compat/AGENTS.md § Running suites](../../AGENTS.md#running-suites-docker--ci)),
which builds the image above — tagged by a content hash of the sources so it
rebuilds only when they change — and runs it, choosing the network mode for
you.

---

## Environment variables

| Variable                       | Default                 | Description                                                                     |
| ------------------------------- | ------------------------ | -------------------------------------------------------------------------------- |
| `OVERCAST_ENDPOINT`             | `http://localhost:4566` | Overcast base URL                                                                |
| `OVERCAST_DEFAULT_REGION`       | `us-east-1`              | AWS region advertised to the SDK                                                 |
| `OVERCAST_REGISTRY_PATH`        | `../registry.json`\*     | Override path to `registry.json`. The Docker image sets this to `/registry.json` |
| `OVERCAST_COMPAT_RUN_ID`        | `local`                  | Prefix for resource names, so concurrent runs and the orphan sweep don't collide |
| `OVERCAST_COMPAT_SKIP_DOCKER`   | unset                    | Set to `1` to drop the `docker` capability, skipping every test the registry marks `requires: [docker]` |
| `OVERCAST_COMPAT_SERVICE`       | unset (all)              | Single AWS service name to run, e.g. `s3`                                       |
| `OVERCAST_COMPAT_GROUPS`        | unset (all)              | Comma-separated group names to run                                              |
| `OVERCAST_COMPAT_TESTS`         | unset (all)              | Comma-separated test names to run within those groups                           |
| `OVERCAST_COMPAT_PARALLEL_SLOTS`| `8`                      | Max groups run concurrently                                                     |
| `OVERCAST_COMPAT_INTERACTIVE`   | unset                    | Set to `1` to run the interactive command protocol instead of one batch run      |

\* Resolved relative to the process working directory when unset; the suite
finds it at `../registry.json` when run from `compat/suites/java-sdk/`.

---

## Architecture

```
java-sdk/
  Dockerfile          ← eclipse-temurin (Maven build stage + JRE runtime stage); see "Via Docker" above
  run.sh              ← builds/runs the Dockerfile; what cmd/compat invokes in CI
  pom.xml             ← AWS SDK v2 BOM + one dependency per service client
  README.md           ← you are here

  src/main/java/io/overcast/compat/
    Main.java             ← entry point: wires clients, merges impls, loads the registry, runs
    clients/
      AwsClients.java     ← lazily-initialised, per-service AWS SDK client factory
    harness/
      TestContext.java    ← per-group state bag (endpoint, region, runId, log)
      TestCase.java       ← one test: name, body, optional op/skip/depends
      TestGroup.java      ← a named group of tests with optional setup/teardown
      TestFn.java         ← functional interface for a test/setup/teardown body
      Runner.java         ← runs groups (bounded concurrency), emits NDJSON to stdout
      InteractiveRunner.java ← the OVERCAST_COMPAT_INTERACTIVE command protocol
      Assertions.java     ← assertion helpers that throw AssertionError with context
    registry/
      Registry.java       ← loads registry.json + registry.generated.json, builds TestGroups
      ScenarioBackend.java ← extension point for executing generated groups (unimplemented)
    groups/
      S3Group.java, SqsGroup.java, DynamoDbGroup.java, …  ← one class per AWS service
      ServiceGroup.java   ← the interface every group class implements

  src/test/java/io/overcast/compat/
    MainRegistrationTest.java          ← the suite's own registrations resolve against registry.json
    registry/RegistryTest.java         ← Registry loader unit tests
    registry/GeneratedRegistryTest.java ← registry.generated.json loading + ScenarioBackend resolution
```

**One class per AWS service**, all implementing `ServiceGroup`. See
[AGENTS.md](AGENTS.md) for the exact shape and how to add one.
