//go:build ignore

// Script: docker-clean-test-networks
// Removes the Docker networks a killed test run left behind.
//
// Usage:
//
//	go run ./scripts/docker-clean-test-networks.go               # make docker-clean-test-networks
//	go run ./scripts/docker-clean-test-networks.go -dry-run      # list without removing
//	go run ./scripts/docker-clean-test-networks.go -min-age 0    # include networks younger than 15 minutes
//
// Every Docker-backed test server mints a pair of networks of its own —
// overcast_ecs_test_<nanotime> and its _control twin, overcast_rds_master_test_<id>
// and its twin — and removes them in t.Cleanup. A test process that is killed,
// or that go test's -timeout panics out of, runs no cleanups, and the pair stays
// for the life of the daemon. Enough of those and the daemon's address pool is
// spent, after which every container test fails as "Docker not available".
//
// What this touches, exactly: networks of that shape (dockertest.IsTestNetwork
// is the rule), with no container attached, created longer ago than -min-age.
// Never overcast, overcast_control, or any network without a _test_ segment —
// a shared instance's planes are not this script's to remove, and neither are
// another agent's. Every removal and every retention is printed with its reason.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/docker"
	"github.com/overcast-sh/overcast/internal/docker/dockertest"
)

func main() {
	socket := flag.String("socket", "", "Docker endpoint (default: LAMBDA_DOCKER_SOCKET, else the platform default)")
	minAge := flag.Duration("min-age", 15*time.Minute,
		"leave an empty network younger than this alone: a test package still running may own it")
	dryRun := flag.Bool("dry-run", false, "print what would be removed without removing it")
	flag.Parse()

	endpoint := *socket
	if endpoint == "" {
		endpoint = os.Getenv("LAMBDA_DOCKER_SOCKET")
	}
	if endpoint == "" {
		endpoint = config.DefaultDockerSocket()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dc := docker.NewClient(endpoint, zap.NewNop())
	if err := dc.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "docker-clean-test-networks: Docker is not reachable at %s: %v\n", endpoint, err)
		os.Exit(1)
	}

	res, err := dockertest.Sweep(ctx, dc, dockertest.SweepOptions{MinAge: *minAge, DryRun: *dryRun},
		func(format string, args ...any) { fmt.Printf(format+"\n", args...) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "docker-clean-test-networks: %v\n", err)
		os.Exit(1)
	}
	verb := "removed"
	if *dryRun {
		verb = "would remove"
	}
	fmt.Printf("docker-clean-test-networks: %s %d network(s), retained %d\n", verb, len(res.Removed), len(res.Retained))
}
