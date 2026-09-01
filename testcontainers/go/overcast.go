// Package overcast provides a Testcontainers module for the Overcast AWS
// emulator: it starts an Overcast container, waits until the emulator answers
// on /_overcast/health, and hands back the endpoint and credentials an AWS SDK
// client needs.
//
// The module is a thin layer over testcontainers-go: every standard
// testcontainers.ContainerCustomizer (WithEnv, WithWaitStrategy, network and
// mount options, …) composes with the options defined here.
package overcast

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// apiPort is the AWS API edge port inside the container.
	apiPort = "4566/tcp"
	// consolePort is the web console port inside the container. Only exposed
	// when WithConsole is used, and only served by the full (non-slim) image.
	consolePort = "4567/tcp"

	// The local-dev credentials Overcast accepts. Any credentials are
	// accepted unless OVERCAST_SIGV4_VALIDATE is on, in which case `test` is
	// the fallback signing secret — so `test`/`test` works in both modes.
	defaultAccessKey = "test"
	defaultSecretKey = "test"
)

// Container is a running Overcast instance started by Run.
type Container struct {
	testcontainers.Container

	region    string
	accountID string
}

// Run starts an Overcast container from img (for example
// "ghcr.io/overcast-sh/overcast-slim:alpha", or a locally built tag) and blocks
// until /_overcast/health answers 200.
//
// Unless the caller sets OVERCAST_HOSTNAME (or one of its LocalStack aliases),
// it is defaulted to the Docker daemon's host so that client-facing URLs the
// emulator returns (SQS queue URLs, …) are dialable from the test process even
// against a remote daemon.
//
// On error the returned *Container may be non-nil so the caller can still
// terminate it; testcontainers.CleanupContainer handles both cases.
func Run(ctx context.Context, img string, opts ...testcontainers.ContainerCustomizer) (*Container, error) {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return nil, fmt.Errorf("docker provider: %w", err)
	}
	defer provider.Close()
	daemonHost, err := provider.DaemonHost(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve docker daemon host: %w", err)
	}

	moduleOpts := []testcontainers.ContainerCustomizer{
		testcontainers.WithExposedPorts(apiPort),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/_overcast/health").
				WithPort(apiPort).
				WithStartupTimeout(2 * time.Minute),
		),
	}
	moduleOpts = append(moduleOpts, opts...)
	// After the caller's options, so it can see whether any hostname variable
	// was set: Overcast fails startup when OVERCAST_HOSTNAME and a LocalStack
	// alias disagree, so the default must not race a caller-provided alias.
	moduleOpts = append(moduleOpts, defaultHostname(daemonHost))

	ctr, err := testcontainers.Run(ctx, img, moduleOpts...)
	var c *Container
	if ctr != nil {
		c = &Container{Container: ctr}
	}
	if err != nil {
		return c, fmt.Errorf("start overcast container: %w", err)
	}

	if err := c.readInfo(ctx); err != nil {
		return c, err
	}
	return c, nil
}

// defaultHostname sets OVERCAST_HOSTNAME to the daemon host unless the caller
// already configured a hostname under any accepted spelling.
func defaultHostname(daemonHost string) testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) error {
		for _, k := range []string{"OVERCAST_HOSTNAME", "LOCALSTACK_HOST", "HOSTNAME_EXTERNAL"} {
			if _, ok := req.Env[k]; ok {
				return nil
			}
		}
		if req.Env == nil {
			req.Env = map[string]string{}
		}
		req.Env["OVERCAST_HOSTNAME"] = daemonHost
		return nil
	}
}

// readInfo fetches /_overcast/info, the authoritative view of the effective
// region and account ID — reading it back rather than re-parsing the request
// env means every alias spelling (DEFAULT_REGION, …) is honoured for free.
func (c *Container) readInfo(ctx context.Context) error {
	endpoint, err := c.APIEndpoint(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_overcast/info", nil)
	if err != nil {
		return fmt.Errorf("build info request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch /_overcast/info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch /_overcast/info: unexpected status %s", resp.Status)
	}
	var info struct {
		Region    string `json:"region"`
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("decode /_overcast/info: %w", err)
	}
	c.region = info.Region
	c.accountID = info.AccountID
	return nil
}

// APIEndpoint returns the AWS API endpoint, e.g. "http://localhost:32771".
// Point AWS_ENDPOINT_URL, an SDK's BaseEndpoint, or the CLI's --endpoint-url
// at it.
func (c *Container) APIEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, apiPort, "http")
}

// ConsoleEndpoint returns the web console URL. It requires the container to
// have been started with WithConsole, from the full (non-slim) image.
func (c *Container) ConsoleEndpoint(ctx context.Context) (string, error) {
	return c.PortEndpoint(ctx, consolePort, "http")
}

// Region returns the emulator's effective default region.
func (c *Container) Region() string { return c.region }

// AccountID returns the account ID the emulator embeds in ARNs.
func (c *Container) AccountID() string { return c.accountID }

// AccessKey returns an access key ID the emulator accepts.
func (c *Container) AccessKey() string { return defaultAccessKey }

// SecretKey returns a secret access key the emulator accepts.
func (c *Container) SecretKey() string { return defaultSecretKey }

// WithConsole additionally exposes the web console port (4567). Use
// ConsoleEndpoint to reach it. The console is only served by the full image —
// the slim image answers with a stub page.
func WithConsole() testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) error {
		req.ExposedPorts = append(req.ExposedPorts, consolePort)
		return nil
	}
}

// WithDockerSocket bind-mounts the host's Docker socket into the container,
// which the container-backed services (Lambda invokes, ECS tasks, RDS/
// ElastiCache/MSK engines, live EFS) need to launch sibling containers.
// Without it those services degrade to metadata-only behaviour. The image's
// entrypoint adjusts socket group permissions itself, so no extra group
// configuration is needed.
func WithDockerSocket() testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) error {
		// The host-side socket path, honouring TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE
		// and rootless/remote daemon configurations. Panics only when no Docker
		// client can be constructed at all, in which case Run fails regardless.
		socket := testcontainers.MustExtractDockerSocket(context.Background())
		prev := req.HostConfigModifier
		req.HostConfigModifier = func(hc *container.HostConfig) {
			if prev != nil {
				prev(hc)
			}
			hc.Binds = append(hc.Binds, socket+":/var/run/docker.sock")
		}
		return nil
	}
}
