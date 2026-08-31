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
planned ([#1495](https://github.com/Neaox/overcast/issues/1495) tracks listing
them in the Testcontainers catalog once Overcast reaches v1.0).

The module starts an Overcast container, waits until `/_overcast/health`
answers, and hands back everything an AWS SDK client needs: the endpoint URL,
region, account ID, and accepted credentials.

## Go

The module lives in this repository as the nested Go module
[`testcontainers/go`](https://github.com/Neaox/overcast/tree/main/testcontainers/go):

```bash
go get github.com/Neaox/overcast/testcontainers/go@main
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

    overcast "github.com/Neaox/overcast/testcontainers/go"
)

func TestWithOvercast(t *testing.T) {
    ctx := context.Background()

    ctr, err := overcast.Run(ctx, "ghcr.io/neaox/overcast-slim:alpha")
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

- `ghcr.io/neaox/overcast-slim:alpha` — API only, smallest and fastest to
  start. The right default for tests.
- `ghcr.io/neaox/overcast:alpha` — adds the web console (handy when debugging
  a failing test interactively; pair with `WithConsole`).

Pin an exact version tag (e.g. `ghcr.io/neaox/overcast-slim:0.0.1-alpha.25`)
in CI — Docker never re-pulls a moving tag it already has, so `:alpha` can go
stale on long-lived runners.

### Options

Every standard `testcontainers.ContainerCustomizer` works (`WithEnv` for the
[configuration variables](./README.md#configuration-reference), network and
mount options, …). The module adds two of its own:

| Option             | Effect                                                                                                                                                              |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `WithDockerSocket` | Bind-mounts the host's Docker socket so container-backed services (Lambda invokes, ECS tasks, RDS/ElastiCache/MSK engines, live EFS) can launch sibling containers. Without it they degrade to metadata-only behaviour. |
| `WithConsole`      | Also exposes the web console port (4567); reach it with `ConsoleEndpoint`. Full image only.                                                                          |

```go
ctr, err := overcast.Run(ctx, "ghcr.io/neaox/overcast:alpha",
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

## Using LocalStack's Testcontainers modules

Pointing an existing LocalStack Testcontainers module at the Overcast image
(e.g. Java's `asCompatibleSubstituteFor`) is **not supported**: those modules
parse the image tag as a LocalStack version to pick legacy behaviours, and
wait for a log line Overcast does not emit. Use this module — or plain
`testcontainers` generic containers with the
`/_overcast/health` wait target — instead. The
[migration guide](./migration-from-localstack.md) covers the rest of a
LocalStack switch-over.
