# Overcast Testcontainers module for Go

A [Testcontainers](https://golang.testcontainers.org/) module for the
[Overcast](https://github.com/Neaox/overcast) AWS emulator: start the emulator
from test code, wait for readiness, and get the endpoint and credentials an
AWS SDK client needs.

```bash
go get github.com/Neaox/overcast/testcontainers/go@main
```

```go
ctr, err := overcast.Run(ctx, "ghcr.io/neaox/overcast-slim:alpha")
testcontainers.CleanupContainer(t, ctr)
if err != nil {
    t.Fatal(err)
}

endpoint, _ := ctr.APIEndpoint(ctx)

client := s3.New(s3.Options{
    BaseEndpoint: aws.String(endpoint),
    Region:       ctr.Region(),
    Credentials:  credentials.NewStaticCredentialsProvider(ctr.AccessKey(), ctr.SecretKey(), ""),
    UsePathStyle: true,
})
```

Full guide, options, and caveats:
[docs/testcontainers.md](../../docs/testcontainers.md).

This is a nested Go module: it versions independently of the emulator and
keeps testcontainers-go out of the main module's dependency tree. Its tests
run in CI against the Docker image built from the same commit
(`OVERCAST_TESTCONTAINERS_IMAGE` overrides the image under test).
