package ecr_test

// registry_persistence_test.go — an image pushed to the emulated ECR is still
// there after Overcast restarts.
//
// This is not a convenience. cdk-assets decides whether to build and push a
// container asset by asking DescribeImages for the asset's content-hash tag
// (cdklabs/cdk-assets, ContainerImageAssetHandler returns early from both
// build() and publish() when it resolves), so a registry that comes back empty
// makes every `cdk deploy` after a restart rebuild and re-push assets that
// never changed — a Docker build and a layer upload per asset, per restart.
//
// The registry is a registry:2 container with AutoRemove, so its storage has to
// outlive it in a named volume for this to hold. The volume is keyed to the
// fixed port's claim, which is why the second server here claims the same port
// as the first.

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func TestDescribeImages_withDocker_pushedImageSurvivesARestart(t *testing.T) {
	skipWithoutDocker(t)

	// Given: a server on a fixed registry port, holding an image someone pushed.
	// The port is what both servers' registries claim, and what names the volume
	// underneath them. Removing that volume afterwards is the harness's job —
	// see WithECRRegistryPort — so nothing here outlives the test.
	port := helpers.ReserveTCPPort(t)
	const repoName = "survives-restart"

	first := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
		helpers.WithECRRegistryPort(port),
	)
	createResp := ecrCall(t, first, "CreateRepository", map[string]any{"repositoryName": repoName})
	repoURI, _ := mustDecode(t, createResp)["repository"].(map[string]any)["repositoryUri"].(string)
	if repoURI == "" {
		t.Fatal("missing repositoryUri")
	}
	dockerLoginOrSkip(t, first)
	target := repoURI + ":restarted"
	runDockerCommand(t, "tag", "registry:2", target)
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "image", "rm", "-f", target).Run()
	})
	runDockerCommand(t, "push", target)

	// When: that server goes away and a new one claims the same port. The store
	// is fresh, so the repository is re-created exactly as `cdk bootstrap`
	// would; only the registry's own storage carries anything over.
	first.Shutdown()
	// The second server's logs are captured and dumped on failure, at Debug,
	// because that is where the answer to a failure here lives. This test went
	// red in CI once with the registry-sweep give-up path unrecorded (issue
	// #1444): NewTestServer's default logger is a Nop, so the sweep's Debug
	// lines naming which give-up path fired — the whole point of the logging
	// added for that issue — were discarded, and the failure was undiagnosable
	// after the fact. Filtered to registry-related messages so a dump is the
	// registry's story, not the server's whole startup.
	core, capturedLogs := observer.New(zap.DebugLevel)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, entry := range capturedLogs.All() {
			if !strings.Contains(entry.Message, "registry") {
				continue
			}
			t.Logf("second server: %s %s %v", entry.Level, entry.Message, entry.ContextMap())
		}
	})
	second := helpers.NewTestServer(t,
		helpers.WithLambdaDocker(),
		helpers.WithRegion("us-east-1"),
		helpers.WithAccountID("000000000000"),
		helpers.WithECRRegistryPort(port),
		helpers.WithLogger(zap.New(core)),
	)
	ecrCall(t, second, "CreateRepository", map[string]any{"repositoryName": repoName}).Body.Close()

	// Then: the publisher is told the image is already there, because it is.
	resp := ecrCall(t, second, "DescribeImages", map[string]any{
		"repositoryName": repoName,
		"imageIds":       []map[string]any{{"imageTag": "restarted"}},
	})
	status := resp.StatusCode
	body := mustDecode(t, resp)
	if status != http.StatusOK {
		t.Fatalf("expected 200 for an image pushed before the restart, got %d (%v)", status, body["__type"])
	}
	details, ok := body["imageDetails"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("expected one imageDetails entry after the restart, got %#v", body["imageDetails"])
	}
}
