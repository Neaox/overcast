---
title: "Testcontainers"
description: "Run Overcast from your integration tests with the Testcontainers module for Go: a few lines to start the emulator, wait for readiness, and point an AWS SDK client at it."
section: "Getting Started"
tags:
  - testcontainers
  - testing
  - docker
  - go
  - overcast
---

# Testcontainers

[Testcontainers](https://testcontainers.com/) starts throwaway Docker
containers from test code and tears them down with the test. Overcast ships a
first-party Testcontainers module for **Go**; modules for other languages are
planned ([#1495](https://github.com/overcast-sh/overcast/issues/1495) tracks listing
them in the Testcontainers catalog once Overcast reaches v1.0).

The module starts an Overcast container, waits until `/_overcast/health`
answers, and hands back everything an AWS SDK client needs: the endpoint URL,
region, account ID, and accepted credentials.

## Go

The module lives in this repository as the nested Go module
[`testcontainers/go`](https://github.com/overcast-sh/overcast/tree/main/testcontainers/go):

```bash
go get github.com/overcast-sh/overcast/testcontainers/go@main
```

(While Overcast is in alpha the module is installed from `main`; tagged module
releases start alongside v1.0.)

```go
import (
    "context"
    "testing"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/testcontainers/testcontainers-go"

    overcast "github.com/overcast-sh/overcast/testcontainers/go"
)

func TestWithOvercast(t *testing.T) {
    ctx := context.Background()

    ctr, err := overcast.Run(ctx, "ghcr.io/overcast-sh/overcast-slim:latest")
    testcontainers.CleanupContainer(t, ctr)
    if err != nil {
        t.Fatal(err)
    }

    endpoint, err := ctr.APIEndpoint(ctx) // e.g. http://localhost:32771
    if err != nil {
        t.Fatal(err)
    }

    client := s3.New(s3.Options{
        BaseEndpoint: aws.String(endpoint),
        Region:       ctr.Region(),
        Credentials:  credentials.NewStaticCredentialsProvider(ctr.AccessKey(), ctr.SecretKey(), ""),
        UsePathStyle: true,
    })
    // ... use the client exactly as against real AWS
}
```

### Which image

- `ghcr.io/overcast-sh/overcast-slim:latest` — API only, smallest and fastest to
  start. The right default for tests.
- `ghcr.io/overcast-sh/overcast:latest` — adds the web console (handy when debugging
  a failing test interactively; pair with `WithConsole`).

Pin an exact version tag (e.g. `ghcr.io/overcast-sh/overcast-slim:0.0.1-alpha.25`)
in CI — Docker never re-pulls a moving tag it already has, so `:latest` (or
`:alpha`) can go stale on long-lived runners.

### Options

Every standard `testcontainers.ContainerCustomizer` works (`WithEnv` for the
[configuration variables](./configuration.md), network and
mount options, …). The module adds two of its own:

| Option             | Effect                                                                                                                                                              |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `WithDockerSocket` | Bind-mounts the host's Docker socket so container-backed services (Lambda invokes, ECS tasks, RDS/ElastiCache/MSK engines, live EFS) can launch sibling containers. Without it they degrade to metadata-only behaviour. |
| `WithConsole`      | Also exposes the web console port (4567); reach it with `ConsoleEndpoint`. Full image only.                                                                          |

```go
ctr, err := overcast.Run(ctx, "ghcr.io/overcast-sh/overcast:latest",
    overcast.WithDockerSocket(),
    overcast.WithConsole(),
    testcontainers.WithEnv(map[string]string{
        "OVERCAST_DEFAULT_REGION": "eu-west-1",
    }),
)
```

### Container API

| Method                 | Returns                                                                       |
| ---------------------- | ----------------------------------------------------------------------------- |
| `APIEndpoint(ctx)`     | The AWS API endpoint on the mapped port — what `AWS_ENDPOINT_URL` would hold |
| `ConsoleEndpoint(ctx)` | The web console URL (requires `WithConsole`)                                  |
| `Region()`             | The emulator's effective default region, read back from `/_overcast/info`     |
| `AccountID()`          | The account ID embedded in ARNs                                               |
| `AccessKey()` / `SecretKey()` | Credentials the emulator accepts (`test`/`test`)                       |

### Port mapping caveats

Testcontainers publishes the edge port on a random host port, so the endpoint
is `http://localhost:<random>`, not `:4566`. Overcast handles the common
consequences itself — with the Docker socket mounted it asks the daemon for
its own port bindings and rewrites client-facing URLs (SQS queue URLs,
split-horizon hostnames) accordingly. What cannot be rewritten are values
baked into signed material at issue time, such as a Cognito token's `iss`
claim: a test that compares those literally needs the container published 1:1
(`docker run -p 4566:4566`) rather than through Testcontainers' random ports.

Unless you set a hostname yourself (`OVERCAST_HOSTNAME` or a LocalStack
alias), the module sets `OVERCAST_HOSTNAME` to the Docker daemon's host, so
returned URLs are dialable from the test process even against a remote
daemon.

## Other languages: the generic-container pattern

Dedicated modules for other languages are planned
([#1495](https://github.com/overcast-sh/overcast/issues/1495)), but nothing about
Overcast requires one — every Testcontainers implementation can run it as a
generic container today. The recipe is always the same three lines of intent:
the image, expose port `4566`, and wait for HTTP 200 on `/_overcast/health`.
Then point the SDK at the mapped port with region `us-east-1` and credentials
`test`/`test` (or read the effective region and account from
`GET /_overcast/info`).

<!-- BEGIN overcast:code-tabs -->

### Node.js

```typescript
import { GenericContainer, Wait } from "testcontainers";

const container = await new GenericContainer("ghcr.io/overcast-sh/overcast-slim:latest")
  .withExposedPorts(4566)
  .withWaitStrategy(Wait.forHttp("/_overcast/health", 4566))
  .start();

const endpoint = `http://${container.getHost()}:${container.getMappedPort(4566)}`;
```

### Java

```java
GenericContainer<?> overcast = new GenericContainer<>("ghcr.io/overcast-sh/overcast-slim:latest")
    .withExposedPorts(4566)
    .waitingFor(Wait.forHttp("/_overcast/health").forPort(4566));
overcast.start();

String endpoint = "http://" + overcast.getHost() + ":" + overcast.getMappedPort(4566);
```

### Python

```python
import requests
from testcontainers.core.container import DockerContainer
from testcontainers.core.waiting_utils import wait_container_is_ready

@wait_container_is_ready(requests.ConnectionError, requests.HTTPError)
def wait_for_health(endpoint: str) -> None:
    requests.get(f"{endpoint}/_overcast/health", timeout=2).raise_for_status()

with DockerContainer("ghcr.io/overcast-sh/overcast-slim:latest").with_exposed_ports(4566) as overcast:
    endpoint = f"http://{overcast.get_container_host_ip()}:{overcast.get_exposed_port(4566)}"
    wait_for_health(endpoint)
```

### .NET

```csharp
var overcast = new ContainerBuilder()
    .WithImage("ghcr.io/overcast-sh/overcast-slim:latest")
    .WithPortBinding(4566, assignRandomHostPort: true)
    .WithWaitStrategy(Wait.ForUnixContainer()
        .UntilHttpRequestIsSucceeded(r => r.ForPort(4566).ForPath("/_overcast/health")))
    .Build();
await overcast.StartAsync();

var endpoint = $"http://{overcast.Hostname}:{overcast.GetMappedPublicPort(4566)}";
```

<!-- END overcast:code-tabs -->

For container-backed services (Lambda invokes, ECS tasks, RDS engines, …) add
your implementation's bind-mount option for `/var/run/docker.sock` — see the
[Docker socket note](../README.md#running-with-docker). Everything else in
this guide (image choice, port-mapping caveats, configuration env vars)
applies unchanged.

## Using LocalStack's Testcontainers modules

Pointing an existing LocalStack Testcontainers module at the Overcast image
(e.g. Java's `asCompatibleSubstituteFor`) is **not supported**: those modules
parse the image tag as a LocalStack version to pick legacy behaviours, and
wait for a log line Overcast does not emit. Use the
[Go module](#go) or the
[generic-container pattern](#other-languages-the-generic-container-pattern)
instead. The [migration guide](./migration-from-localstack.md) covers the
rest of a LocalStack switch-over.
