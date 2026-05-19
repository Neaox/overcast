# compat/suites/cli — AWS CLI v2

This suite drives all Overcast operations via the `aws` CLI instead of an SDK.
It tests the same service operations as the other suites to verify command-line
compatibility.

## Prerequisites

### AWS CLI v2

The devcontainer installs AWS CLI v2 automatically (pinned in
`.devcontainer/Dockerfile`). The pinned version is also recorded in
[`requirements.txt`](requirements.txt) alongside the manual install command.

To install manually inside the running container:

```bash
curl -sSfL https://awscli.amazonaws.com/awscli-exe-linux-x86_64-2.24.21.zip \
  -o /tmp/awscli.zip \
  && unzip -q /tmp/awscli.zip -d /tmp \
  && sudo /tmp/aws/install \
  && rm -rf /tmp/awscli.zip /tmp/aws
```

Verify:

```bash
aws --version
# aws-cli/2.24.21 Python/... Linux/...
```

To upgrade the pinned version, bump `AWS_CLI_VERSION` in
`.devcontainer/Dockerfile` and update the version strings in
`compat/suites/cli/requirements.txt`, then rebuild the container.

### Other requirements

- Overcast running on `http://localhost:4566` (or set `OVERCAST_ENDPOINT`)
- Go 1.22+ (for the Go test runner)

## Running the suite

```bash
# From workspace root
make compat-serve

# Or run the CLI suite alone
cd compat/suites/cli && go run ./cmd/runner
```

## Environment variables

| Variable                  | Default                 | Description       |
| ------------------------- | ----------------------- | ----------------- |
| `OVERCAST_ENDPOINT`       | `http://localhost:4566` | Emulator endpoint |
| `OVERCAST_DEFAULT_REGION` | `us-east-1`             | AWS region        |
| `OVERCAST_COMPAT_RUN_ID`  | `local`                 | Run identifier    |

No real AWS credentials are needed — the runner passes `--no-sign-request` to
every `aws` command.
